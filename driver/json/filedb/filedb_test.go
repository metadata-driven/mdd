package filedb_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/metadata-driven/mdd/driver"
	_ "github.com/metadata-driven/mdd/driver/json"
	_ "github.com/metadata-driven/mdd/driver/yaml"
)

func testSchema(path string) *driver.Database {
	return &driver.Database{Connection: path, Tables: []*driver.Table{{Name: "users", Columns: []*driver.Column{
		{Name: "name", Type: driver.TypeString}, {Name: "active", Type: driver.TypeBoolean},
		{Name: "age", Type: driver.TypeInteger}, {Name: "balance", Type: driver.TypeDecimal, Precision: 20, Scale: 6},
		{Name: "friend", Type: driver.TypeID}, {Name: "ava", Type: driver.TypeFile}, {Name: "data", Type: driver.TypeJSON},
	}}}}
}

func open(t *testing.T, schema *driver.Database) driver.Connection {
	t.Helper()
	c, err := driver.NewConnection(schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func tx(t *testing.T, c driver.Connection) driver.Transaction {
	t.Helper()
	x, err := c.OpenTransaction(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(x.Rollback)
	return x
}

func TestFileDrivers(t *testing.T) {
	for _, ext := range []string{"json", "yaml", "yml"} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data."+ext)
			schema := testSchema(path)
			c := open(t, schema)
			x := tx(t, c)
			id, err := x.Set("users", 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			values := driver.DataObject{"name": "Alice", "active": true, "age": int64(16), "balance": "12345678901234.123456", "friend": uint64(99), "ava": []byte{0, 255, 1}, "data": map[string]any{"nested": []any{"text", true}}}
			if _, err = x.Set("users", id, values); err != nil {
				t.Fatal(err)
			}
			if err = x.Commit(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "Alice") {
				t.Fatalf("file has no record: %s", data)
			}
			if err = c.Close(); err != nil {
				t.Fatal(err)
			}
			c = open(t, schema)
			x = tx(t, c)
			row, err := x.Get("users", id)
			if err != nil {
				t.Fatal(err)
			}
			avatar, ok := row["ava"].(driver.File)
			if !ok {
				t.Fatalf("avatar type = %T, want driver.File", row["ava"])
			}
			row["ava"] = avatar.Bytes()
			if !reflect.DeepEqual(row, values) {
				t.Fatalf("row = %#v; want %#v", row, values)
			}
			if _, err = x.Set("users", id, driver.DataObject{"name": "rolled back"}); err != nil {
				t.Fatal(err)
			}
			x.Rollback()
			c = open(t, schema)
			x = tx(t, c)
			row, err = x.Get("users", id)
			if err != nil || row["name"] != "Alice" {
				t.Fatalf("after rollback = %#v, %v", row, err)
			}
			if err = x.Delete("users", id); err != nil {
				t.Fatal(err)
			}
			if err = x.Commit(); err != nil {
				t.Fatal(err)
			}
			c = open(t, schema)
			x = tx(t, c)
			next, err := x.Set("users", 0, nil)
			if err != nil || next != id {
				t.Fatalf("next ID = %d, %v", next, err)
			}
		})
	}
}
func TestLoadExistingDocument(t *testing.T) {
	for _, tc := range []struct{ ext, doc string }{
		{"json", `{"users":[{"id":9007199254740993,"name":"Alice","age":16,"data":{"number":9007199254740993}}]}`},
		{"yaml", "users:\n  - id: 9007199254740993\n    name: Alice\n    age: 16\n    data:\n      number: 9007199254740993\n"},
	} {
		t.Run(tc.ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data."+tc.ext)
			if err := os.WriteFile(path, []byte(tc.doc), 0600); err != nil {
				t.Fatal(err)
			}
			c := open(t, testSchema(path))
			x := tx(t, c)
			row, err := x.Get("users", 9007199254740993)
			if err != nil || row["name"] != "Alice" || row["age"] != int64(16) {
				t.Fatalf("row = %#v, %v", row, err)
			}
			if row["balance"] != "0" || row["active"] != false {
				t.Fatalf("missing column defaults = %#v", row)
			}
			value := row["data"].(map[string]any)["number"]
			if fmt.Sprint(value) != "9007199254740993" {
				t.Fatalf("number = %v", value)
			}
			if tc.ext == "json" {
				if _, ok := value.(json.Number); !ok {
					t.Fatalf("number type = %T", value)
				}
			}
			id, err := x.Set("users", 0, nil)
			if err != nil || id != 9007199254740994 {
				t.Fatalf("id = %d, %v", id, err)
			}
		})
	}
}
func TestFileErrors(t *testing.T) {
	for _, ext := range []string{"json", "yaml"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "data."+ext)
			if err := os.WriteFile(path, []byte("[broken"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := driver.NewConnection(testSchema(path)); err == nil {
				t.Fatal("invalid document accepted")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			c := open(t, testSchema(path))
			x := tx(t, c)
			if _, err := x.Set("users", 0, nil); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			if err := x.Commit(); err == nil {
				t.Fatal("commit to directory succeeded")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			x = tx(t, c)
			row, err := x.Get("users", 1)
			if !errors.Is(err, driver.ErrNotFound) || row != nil {
				t.Fatalf("failed commit published row %#v, %v", row, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected file after failed commit: %v", err)
			}
		})
	}
}
