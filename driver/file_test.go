package driver_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metadata-driven/mdd/driver"
	_ "github.com/metadata-driven/mdd/driver/file"
	_ "github.com/metadata-driven/mdd/driver/json"
	_ "github.com/metadata-driven/mdd/driver/mem"
	_ "github.com/metadata-driven/mdd/driver/mysql"
	_ "github.com/metadata-driven/mdd/driver/sqlite"
	_ "github.com/metadata-driven/mdd/driver/yaml"
)

var errBinaryRead = errors.New("binary read failed")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errBinaryRead }

type trackedReader struct {
	io.Reader
	closed *int
}

func (r trackedReader) Close() error {
	*r.closed++
	return nil
}

func TestNewFileInputs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "string", value: "hello"},
		{name: "bytes", value: []byte("hello")},
		{name: "factory", value: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("hello")), nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := driver.NewFile(tc.value)
			for range 2 {
				if got := value.String(); got != "hello" {
					t.Fatalf("value = %q", got)
				}
			}
		})
	}

	t.Run("reader", func(t *testing.T) {
		value := driver.NewFile(strings.NewReader("hello"))
		data, err := io.ReadAll(value)
		if err != nil || string(data) != "hello" {
			t.Fatalf("read = %q, %v", data, err)
		}
		if _, err := value.Read(make([]byte, 1)); !errors.Is(err, driver.ErrFileConsumed) {
			t.Fatalf("reopen error = %v", err)
		}
	})

	t.Run("read closer", func(t *testing.T) {
		closed := 0
		value := driver.NewFile(trackedReader{Reader: strings.NewReader("hello"), closed: &closed})
		if err := value.Close(); err != nil || closed != 0 {
			t.Fatalf("close before reading = %v, close count = %d", err, closed)
		}
		buf := make([]byte, 2)
		if n, err := value.Read(buf); n != 2 || err != nil || string(buf) != "he" {
			t.Fatalf("read = %q, %d, %v", buf, n, err)
		}
		if err := value.Close(); err != nil || closed != 1 {
			t.Fatalf("close = %v, close count = %d", err, closed)
		}
		if _, err := value.Read(buf); !errors.Is(err, driver.ErrFileConsumed) {
			t.Fatalf("reopen error = %v", err)
		}
	})

	t.Run("failed reader", func(t *testing.T) {
		value := driver.NewFile(failingReader{})
		buf := make([]byte, 1)
		if _, err := value.Read(buf); !errors.Is(err, errBinaryRead) {
			t.Fatalf("read error = %v", err)
		}
		if _, err := value.Read(buf); !errors.Is(err, driver.ErrFileConsumed) {
			t.Fatalf("reopen error = %v", err)
		}
	})

	if driver.NewFile(nil).Bytes() != nil || driver.NewFile([]byte(nil)).Bytes() != nil {
		t.Fatal("nil binary source is not absent")
	}
	if data := driver.NewFile([]byte{}).Bytes(); data == nil || len(data) != 0 {
		t.Fatalf("empty binary = %#v", data)
	}
	t.Run("unsupported source", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("unsupported source did not panic")
			}
		}()
		driver.NewFile(42)
	})
}

func TestFileStream(t *testing.T) {
	opens, closes := 0, 0
	value := driver.NewFile(func() (io.ReadCloser, error) {
		opens++
		return trackedReader{Reader: strings.NewReader("hello"), closed: &closes}, nil
	})
	if opens != 0 {
		t.Fatal("binary was opened eagerly")
	}
	buf := make([]byte, 2)
	if n, err := value.Read(buf); n != 2 || err != nil || string(buf) != "he" {
		t.Fatalf("first read = %q, %d, %v", buf, n, err)
	}
	if opens != 1 || closes != 0 {
		t.Fatalf("opened %d times, closed %d times", opens, closes)
	}
	if got := value.String(); got != "llo" {
		t.Fatalf("remaining value = %q", got)
	}
	if opens != 1 || closes != 1 {
		t.Fatalf("after EOF: opened %d times, closed %d times", opens, closes)
	}
	if got := value.String(); got != "hello" || opens != 2 || closes != 2 {
		t.Fatalf("reopened value = %q, opens = %d, closes = %d", got, opens, closes)
	}
	if err := value.Close(); err != nil || opens != 2 {
		t.Fatalf("close before reopening = %v, opens = %d", err, opens)
	}
	if n, err := value.Read(buf); n != 2 || err != nil || string(buf) != "he" || opens != 3 {
		t.Fatalf("read after close = %q, %d, %v, opens = %d", buf, n, err, opens)
	}
	if err := value.Close(); err != nil || closes != 3 {
		t.Fatalf("close after reopening = %v, closes = %d", err, closes)
	}

	var attempts int
	retry := driver.NewFile(func() (io.ReadCloser, error) {
		attempts++
		if attempts == 1 {
			return nil, errBinaryRead
		}
		return io.NopCloser(strings.NewReader("ok")), nil
	})
	if _, err := retry.Read(buf); !errors.Is(err, errBinaryRead) {
		t.Fatalf("open error = %v", err)
	}
	if n, err := retry.Read(buf); n != 2 || err != nil || string(buf) != "ok" {
		t.Fatalf("retry = %q, %d, %v", buf, n, err)
	}
	if err := retry.Close(); err != nil {
		t.Fatal(err)
	}

	opens, closes = 0, 0
	readError := driver.NewFile(func() (io.ReadCloser, error) {
		opens++
		if opens == 1 {
			return trackedReader{Reader: failingReader{}, closed: &closes}, nil
		}
		return trackedReader{Reader: strings.NewReader("ok"), closed: &closes}, nil
	})
	if _, err := readError.Read(buf); !errors.Is(err, errBinaryRead) || closes != 1 {
		t.Fatalf("read error = %v, closes = %d", err, closes)
	}
	if n, err := readError.Read(buf); n != 2 || err != nil || string(buf) != "ok" {
		t.Fatalf("read after error = %q, %d, %v", buf, n, err)
	}
	if err := readError.Close(); err != nil || closes != 2 {
		t.Fatalf("close after retry = %v, closes = %d", err, closes)
	}

	absent := driver.NewFile(func() (io.ReadCloser, error) { return nil, nil })
	empty := driver.NewFile(func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil })
	if absent.Bytes() != nil || empty.Bytes() == nil {
		t.Fatal("absent and empty binary values are not distinguished")
	}
}

func TestFileAndBinaryTransactions(t *testing.T) {
	for _, name := range []string{"mem", "sqlite", "file", "json", "yaml", "mysql"} {
		t.Run(name, func(t *testing.T) {
			testFileAndBinaryTransaction(t, name)
		})
	}
}

func testFileAndBinaryTransaction(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	connection := map[string]string{
		"mem":    "mem:",
		"sqlite": filepath.Join(dir, "data.sqlite"),
		"file":   "file:" + filepath.Join(dir, "files"),
		"json":   filepath.Join(dir, "data.json"),
		"yaml":   filepath.Join(dir, "data.yaml"),
	}[name]
	table := "records"
	if name == "mysql" {
		connection = "mysql://root@/testdb"
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			t.Fatal(err)
		}
		table = fmt.Sprintf("mdd_binary_%x", suffix)
	}
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
	if name == "mysql" {
		db, err := sql.Open("mysql", strings.TrimPrefix(connection, "mysql://"))
		if err != nil {
			t.Fatal(err)
		}
		for column, want := range map[string]string{"raw": "longblob", "first": "longblob"} {
			var got string
			if err := db.QueryRow("SELECT DATA_TYPE FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?", table, column).Scan(&got); err != nil || got != want {
				t.Fatalf("MySQL column %s = %q, %v; want %s", column, got, err, want)
			}
		}
		t.Cleanup(func() {
			if _, err := db.Exec("DROP TABLE IF EXISTS `" + table + "`"); err != nil {
				t.Error(err)
			}
			_ = db.Close()
		})
	}
	t.Cleanup(func() { _ = conn.Close() })

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

	if _, err := tx.Set(table, id, driver.DataObject{"name": "changed", "first": failingReader{}}); !errors.Is(err, errBinaryRead) {
		t.Fatalf("failed binary write = %v", err)
	}
	row, err = tx.Get(table, id)
	if err != nil || row["name"] != "Alice" {
		t.Fatalf("failed Set changed the row: %#v, %v", row, err)
	}
	if got := fileField(t, row, "first").Bytes(); !bytes.Equal(got, payload) {
		t.Fatalf("failed Set changed the binary value; length = %d", len(got))
	}
	if name == "sqlite" || name == "mysql" {
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
	if name != "mem" {
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		conn, err = driver.NewConnection(schema)
		if err != nil {
			t.Fatal(err)
		}
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

func TestBinaryAndFileIndexes(t *testing.T) {
	for _, name := range []string{"mem", "file", "json", "yaml"} {
		for _, typ := range []driver.DataType{driver.TypeBinary, driver.TypeFile} {
			t.Run(fmt.Sprintf("%s/%d", name, typ), func(t *testing.T) {
				dir := t.TempDir()
				connection := map[string]string{"mem": "mem:", "file": "file:" + filepath.Join(dir, "files"), "json": filepath.Join(dir, "data.json"), "yaml": filepath.Join(dir, "data.yaml")}[name]
				schema := &driver.Database{Connection: connection, Tables: []*driver.Table{{Name: "records", Columns: []*driver.Column{{Name: "data", Type: typ}}, Indexes: []*driver.Index{{Name: "by_data", Columns: []string{"data"}}}}}}
				conn, err := driver.NewConnection(schema)
				if typ == driver.TypeFile {
					if err == nil {
						_ = conn.Close()
						t.Fatal("expected unsupported index error")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					_ = conn.Close()
				}
			})
		}
	}
}
