package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/driver/internal/drivertest"
	_ "github.com/metadata-driven/mdd/driver/sqlite"
)

func schema(path string) *driver.Database {
	return &driver.Database{Connection: path, Tables: []*driver.Table{{Name: "users", Columns: []*driver.Column{{Name: "name", Type: driver.TypeString}, {Name: "active", Type: driver.TypeBoolean}, {Name: "age", Type: driver.TypeInteger}, {Name: "balance", Type: driver.TypeDecimal, Precision: 20, Scale: 6}, {Name: "friend", Type: driver.TypeID}, {Name: "data", Type: driver.TypeJSON}, {Name: "ava", Type: driver.TypeFile}}, Indexes: []*driver.Index{{Name: "by_age", Columns: []string{"age"}}}}}}
}
func conn(t *testing.T, s *driver.Database) driver.Connection {
	t.Helper()
	c, e := driver.NewConnection(s)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
func trans(t *testing.T, c driver.Connection) driver.Transaction {
	t.Helper()
	x, e := c.OpenTransaction(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(x.Rollback)
	return x
}
func id(t *testing.T, x driver.Transaction) uint64 {
	t.Helper()
	n, e := x.Set("users", 0, nil)
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func set(t *testing.T, x driver.Transaction, n uint64, v driver.DataObject) {
	t.Helper()
	if _, e := x.Set("users", n, v); e != nil {
		t.Fatal(e)
	}
}
func commit(t *testing.T, x driver.Transaction) {
	t.Helper()
	if e := x.Commit(); e != nil {
		t.Fatal(e)
	}
}
func get(t *testing.T, x driver.Transaction, n uint64) driver.DataObject {
	t.Helper()
	v, e := x.Get("users", n)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestValuesAndPersistence(t *testing.T) {
	s := schema(filepath.Join(t.TempDir(), "test.sqlite"))
	c := conn(t, s)
	x := trans(t, c)
	a, b := id(t, x), id(t, x)
	set(t, x, a, driver.DataObject{"name": "Alice \x00 '", "active": true, "age": 16, "balance": "12345678901234.123456", "friend": b, "ava": []byte{0, 255, 1}, "data": map[string]any{"nested": []any{"a", true}}})
	if err := x.Increment("users", a, driver.DataObject{"balance": "0.000001", "age": 2}); err != nil {
		t.Fatal(err)
	}
	commit(t, x)
	x = trans(t, c)
	row := get(t, x, a)
	avatar, ok := row["ava"].(driver.File)
	if !ok {
		t.Fatalf("avatar type = %T, want driver.File", row["ava"])
	}
	row["ava"] = avatar.Bytes()
	want := driver.DataObject{"name": "Alice \x00 '", "active": true, "age": int64(18), "balance": "12345678901234.123457", "friend": b, "ava": []byte{0, 255, 1}, "data": map[string]any{"nested": []any{"a", true}}}
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("got %#v want %#v", row, want)
	}
	set(t, x, a, driver.DataObject{"name": "rollback"})
	x.Rollback()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = conn(t, s)
	x = trans(t, c)
	if get(t, x, a)["name"] != "Alice \x00 '" {
		t.Fatal("rollback or persistence failed")
	}
	if row, err := x.Get("users", 999); row != nil || !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("missing record = %#v, %v", row, err)
	}
	if err := x.Delete("users", b); err != nil {
		t.Fatal(err)
	}
	commit(t, x)
}
func TestDecimalPrecisionAndOrder(t *testing.T) {
	c := conn(t, schema(filepath.Join(t.TempDir(), "test.db")))
	x := trans(t, c)
	vals := []string{"99999999999999.999999", "-99999999999999.999999", "0.000000", "-2.000001", "2.000000"}
	for _, v := range vals {
		n := id(t, x)
		set(t, x, n, driver.DataObject{"balance": v})
	}
	commit(t, x)
	x = trans(t, c)
	q := &driver.Query{Table: "users", Order: []driver.QueryOrder{{Column: "balance"}}, Limit: 2}
	var all []uint64
	for range 3 {
		offset, e := x.Fetch(q, func(n uint64) error { all = append(all, n); return nil })
		if e != nil {
			t.Fatal(e)
		}
		q.Offset = offset
	}
	if !slices.Equal(all, []uint64{2, 4, 3, 5, 1}) {
		t.Fatalf("ordered IDs = %v", all)
	}
	if err := x.Increment("users", 1, driver.DataObject{"balance": "0.000001"}); err == nil {
		t.Fatal("decimal overflow accepted")
	}
	x.Rollback()
}
func TestFetchAndInterrupt(t *testing.T) {
	c := conn(t, schema(filepath.Join(t.TempDir(), "test.db")))
	x := trans(t, c)
	for i := 1; i <= 20; i++ {
		n := id(t, x)
		set(t, x, n, driver.DataObject{"age": i % 3, "name": fmt.Sprintf("user-%d", i)})
	}
	commit(t, x)
	x = trans(t, c)
	q := &driver.Query{Table: "users", Index: "by_age", Limit: 3}
	var got []uint64
	offset, e := x.Fetch(q, func(n uint64) error {
		got = append(got, n)
		if len(got) == 2 {
			return fmt.Errorf("stop: %w", driver.ErrBreak)
		}
		return nil
	})
	if e != nil || offset != "6" || !slices.Equal(got, []uint64{3, 6}) {
		t.Fatalf("interrupted %v %s %v", got, offset, e)
	}
	q.Offset = offset
	got = nil
	_, e = x.Fetch(q, func(n uint64) error { got = append(got, n); return nil })
	if e != nil || !slices.Equal(got, []uint64{9, 12, 15}) {
		t.Fatalf("resumed %v %v", got, e)
	}
	failure := errors.New("callback")
	_, e = x.Fetch(&driver.Query{Table: "users"}, func(uint64) error { return failure })
	if !errors.Is(e, failure) {
		t.Fatalf("callback error = %v", e)
	}
	q = &driver.Query{Table: "users", Order: []driver.QueryOrder{{Column: "id", Desc: true}}, Limit: 3}
	q.Filters = append(q.Filters, driver.QueryFilter{Column: "age", Op: driver.OpEqual, Values: []any{1}})
	got = nil
	_, e = x.Fetch(q, func(n uint64) error { got = append(got, n); return nil })
	if e != nil || !slices.Equal(got, []uint64{19, 16, 13}) {
		t.Fatalf("filtered %v %v", got, e)
	}
}
func TestConcurrentIncrements(t *testing.T) {
	c := conn(t, schema(filepath.Join(t.TempDir(), "test.db")))
	x := trans(t, c)
	n := id(t, x)
	commit(t, x)
	const workers = 8
	const count = 40
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < count; i++ {
				for attempts := 0; ; attempts++ {
					if attempts > 100 {
						errs <- errors.New("too many lock retries")
						return
					}
					x, e := c.OpenTransaction(context.Background())
					if e != nil {
						if driver.IsLockError(e) {
							time.Sleep(time.Millisecond)
							continue
						}
						errs <- e
						return
					}
					e = x.Increment("users", n, driver.DataObject{"age": 1, "balance": "0.1"})
					if e == nil {
						e = x.Commit()
					} else {
						x.Rollback()
					}
					if e == nil {
						break
					}
					if !driver.IsLockError(e) {
						errs <- e
						return
					}
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	x = trans(t, c)
	row := get(t, x, n)
	if row["age"] != int64(workers*count) || row["balance"] != "32.000000" {
		t.Fatalf("result %#v", row)
	}
}
func TestSchemaAndContexts(t *testing.T) {
	s := schema(filepath.Join(t.TempDir(), "test.db"))
	c := conn(t, s)
	x := trans(t, c)
	n := id(t, x)
	commit(t, x)
	c.Close()
	s.Tables[0].Columns = append(s.Tables[0].Columns, &driver.Column{Name: "added", Type: driver.TypeString})
	c = conn(t, s)
	x = trans(t, c)
	if get(t, x, n)["added"] != "" {
		t.Fatal("column not added")
	}
	x.Rollback()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.OpenTransaction(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("context error %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	s.Tables[0].ColumnByName("balance").Scale = 2
	if _, err := driver.NewConnection(s); err == nil {
		t.Fatal("incompatible decimal schema accepted")
	}
}

func TestConcurrentReadAndSnapshotConflict(t *testing.T) {
	s := schema(filepath.Join(t.TempDir(), "test.sqlite"))
	first := conn(t, s)
	second := conn(t, s)
	write := trans(t, first)
	n := id(t, write)
	set(t, write, n, driver.DataObject{"name": "before"})
	commit(t, write)
	reader := trans(t, first)
	if get(t, reader, n)["name"] != "before" {
		t.Fatal("initial read failed")
	}
	writer := trans(t, second)
	set(t, writer, n, driver.DataObject{"name": "after"})
	commit(t, writer)
	if get(t, reader, n)["name"] != "before" {
		t.Fatal("read snapshot changed")
	}
	if _, err := reader.Set("users", n, driver.DataObject{"name": "conflict"}); !driver.IsLockError(err) {
		t.Fatalf("snapshot write error = %v", err)
	}
	reader.Rollback()
	reader = trans(t, first)
	if get(t, reader, n)["name"] != "after" {
		t.Fatal("committed value not visible")
	}
}

func TestBinaryOrderPagination(t *testing.T) {
	c := conn(t, schema(filepath.Join(t.TempDir(), "test.db")))
	x := trans(t, c)
	for _, v := range []any{[]byte{2}, nil, []byte{1}} {
		n := id(t, x)
		set(t, x, n, driver.DataObject{"ava": v})
	}
	commit(t, x)
	x = trans(t, c)
	q := &driver.Query{Table: "users", Order: []driver.QueryOrder{{Column: "ava"}}, Limit: 1}
	var got []uint64
	for range 3 {
		offset, err := x.Fetch(q, func(n uint64) error { got = append(got, n); return nil })
		if err != nil {
			t.Fatal(err)
		}
		q.Offset = offset
	}
	if !slices.Equal(got, []uint64{2, 3, 1}) {
		t.Fatalf("binary order = %v", got)
	}
}

func TestFileAndBinaryTransactions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sqlite")
	drivertest.FileAndBinaryTransactions(t, path, "records", drivertest.FileOptions{
		Reopen:        true,
		LiveFileReads: true,
	})
}
