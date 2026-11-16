package drivertest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/metadata-driven/mdd/driver"
)

type FileOptions struct {
	Reopen        bool
	LiveFileReads bool
	AfterOpen     func(*testing.T)
}

// FileAndBinaryTransactions checks file and binary behavior through the driver API.
func FileAndBinaryTransactions(t *testing.T, connection, table string, opts FileOptions) {
	t.Helper()
	errBinaryRead := errors.New("binary read failed")
	schema := &driver.Database{Connection: connection, Tables: []*driver.Table{{
		Name: table,
		Columns: []*driver.Column{
			{Name: "name", Type: driver.TypeString},
			{Name: "raw", Type: driver.TypeBinary},
			{Name: "raw_empty", Type: driver.TypeBinary},
			{Name: "first", Type: driver.TypeFile},
			{Name: "second", Type: driver.TypeFile},
			{Name: "empty", Type: driver.TypeFile},
			{Name: "null_value", Type: driver.TypeFile},
		},
	}}}
	conn, err := driver.NewConnection(schema)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if opts.AfterOpen != nil {
		opts.AfterOpen(t)
	}

	ctx := context.Background()
	tx, err := conn.OpenTransaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	id, err := tx.Set(table, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Set(table, id, driver.DataObject{"raw": strings.NewReader("invalid")}); err == nil {
		t.Fatal("binary field accepted a reader")
	}
	payload := bytes.Repeat([]byte{0, 1, 255}, 50000)
	opens := 0
	source := driver.NewFile(func() (io.ReadCloser, error) {
		opens++
		return io.NopCloser(strings.NewReader("second value")), nil
	})
	if _, err := tx.Set(table, id, driver.DataObject{
		"name": "Alice", "first": bytes.NewReader(payload), "second": source,
		"empty": []byte{}, "null_value": nil, "raw": []byte{0, 1, 255, 42}, "raw_empty": []byte{},
	}); err != nil {
		t.Fatal(err)
	}
	if opens != 1 {
		t.Fatalf("Set opened its binary input %d times", opens)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx, err = conn.OpenTransaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	row, err := tx.Get(table, id)
	if err != nil {
		t.Fatal(err)
	}
	if row["name"] != "Alice" || row["null_value"] != nil || row["empty"] != nil {
		t.Fatalf("unexpected row: %#v", row)
	}
	if got, ok := row["raw"].([]byte); !ok || !bytes.Equal(got, []byte{0, 1, 255, 42}) {
		t.Fatalf("raw binary = %#v", row["raw"])
	}
	if row["raw_empty"] != nil {
		t.Fatalf("empty raw binary = %#v", row["raw_empty"])
	}
	first := fileField(t, row, "first")
	second := fileField(t, row, "second")
	data, err := io.ReadAll(first)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("first binary length = %d, error = %v", len(data), err)
	}
	if got := second.String(); got != "second value" {
		t.Fatalf("second binary = %q", got)
	}
	if _, err := tx.Set(table, id, driver.DataObject{"second": first}); err != nil {
		t.Fatal(err)
	}
	row, err = tx.Get(table, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileField(t, row, "second").Bytes(); !bytes.Equal(got, payload) {
		t.Fatalf("copied binary length = %d", len(got))
	}
	if _, err := tx.Set(table, id, driver.DataObject{"first": payload, "second": "text input"}); err != nil {
		t.Fatal(err)
	}
	row, err = tx.Get(table, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileField(t, row, "second").String(); got != "text input" {
		t.Fatalf("string input = %q", got)
	}
	for _, source := range []any{"wrapped string", []byte("wrapped bytes"), strings.NewReader("wrapped reader")} {
		if _, err := tx.Set(table, id, driver.DataObject{"second": driver.NewFile(source)}); err != nil {
			t.Fatal(err)
		}
		row, err = tx.Get(table, id)
		if err != nil {
			t.Fatal(err)
		}
		if got := fileField(t, row, "second").String(); !strings.HasPrefix(got, "wrapped ") {
			t.Fatalf("wrapped input %T = %q", source, got)
		}
	}

	if _, err := tx.Set(table, id, driver.DataObject{"name": "changed", "first": iotest.ErrReader(errBinaryRead)}); !errors.Is(err, errBinaryRead) {
		t.Fatalf("failed binary write = %v", err)
	}
	row, err = tx.Get(table, id)
	if err != nil || row["name"] != "Alice" {
		t.Fatalf("failed Set changed the row: %#v, %v", row, err)
	}
	if got := fileField(t, row, "first").Bytes(); !bytes.Equal(got, payload) {
		t.Fatalf("failed Set changed the binary value; length = %d", len(got))
	}
	if opts.LiveFileReads {
		prior := fileField(t, row, "second")
		if _, err := tx.Set(table, id, driver.DataObject{"second": "a longer value"}); err != nil {
			t.Fatal(err)
		}
		if got := prior.String(); got != "a longer value" {
			t.Fatalf("updated binary = %q", got)
		}
		current := fileField(t, row, "first")
		part := make([]byte, 3)
		if n, err := current.Read(part); n != len(part) || err != nil {
			t.Fatalf("first chunk = %d, %v", n, err)
		}
		if _, err := tx.Set(table, id, driver.DataObject{"name": "Alice"}); err != nil {
			t.Fatal(err)
		}
		if n, err := current.Read(part); n != len(part) || err != nil {
			t.Fatalf("read after unrelated update = %d, %v", n, err)
		}
		if _, err := tx.Set(table, id, driver.DataObject{"first": "replacement"}); err != nil {
			t.Fatal(err)
		}
		remaining, err := io.ReadAll(current)
		if err != nil || !bytes.Equal(remaining, payload[6:]) {
			t.Fatalf("remaining binary length = %d, error = %v", len(remaining), err)
		}
		if got := current.String(); got != "replacement" {
			t.Fatalf("reopened binary = %q", got)
		}
	}
	if _, err := tx.Set(table, id, driver.DataObject{"first": nil}); err != nil {
		t.Fatal(err)
	}
	row, err = tx.Get(table, id)
	if err != nil || row["first"] != nil {
		t.Fatalf("removed binary = %#v, %v", row["first"], err)
	}
	tx.Rollback()

	tx, err = conn.OpenTransaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	row, err = tx.Get(table, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileField(t, row, "first").Bytes(); !bytes.Equal(got, payload) {
		t.Fatalf("rollback changed the binary value; length = %d", len(got))
	}
	tx.Rollback()
	if opts.Reopen {
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := driver.NewConnection(schema)
		if err != nil {
			t.Fatal(err)
		}
		conn = reopened
		tx, err = conn.OpenTransaction(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		row, err = tx.Get(table, id)
		if err != nil {
			t.Fatal(err)
		}
		if got := fileField(t, row, "first").Bytes(); !bytes.Equal(got, payload) {
			t.Fatalf("reopened binary length = %d", len(got))
		}
		if got, ok := row["raw"].([]byte); !ok || !bytes.Equal(got, []byte{0, 1, 255, 42}) {
			t.Fatalf("reopened raw binary = %#v", row["raw"])
		}
		if got := fileField(t, row, "second").String(); got != "second value" {
			t.Fatalf("reopened second binary = %q", got)
		}
	}
}

func fileField(t *testing.T, row driver.DataObject, name string) driver.File {
	t.Helper()
	value, ok := row[name].(driver.File)
	if !ok {
		t.Fatalf("%s = %T, want driver.File", name, row[name])
	}
	return value
}
