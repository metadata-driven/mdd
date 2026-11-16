package mem_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/driver/mem"
)

func schema() *driver.Database {
	return &driver.Database{Tables: []*driver.Table{{
		Name: "users",
		Columns: []*driver.Column{
			{Name: "name", Type: driver.TypeString},
			{Name: "active", Type: driver.TypeBoolean},
			{Name: "age", Type: driver.TypeInteger},
			{Name: "balance", Type: driver.TypeDecimal, Precision: 20, Scale: 6},
			{Name: "birthday", Type: driver.TypeDatetime},
			{Name: "ava", Type: driver.TypeFile},
			{Name: "friend", Type: driver.TypeID},
			{Name: "data", Type: driver.TypeJSON},
		},
		Indexes: []*driver.Index{{Name: "by_age", Type: driver.IndexTypeIndex, Columns: []string{"age", "name"}}},
	}}}
}

func newConn(t *testing.T) driver.Connection {
	t.Helper()
	conn, err := driver.NewConnection(schema())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	return conn
}

func transaction(t *testing.T, conn driver.Connection) driver.Transaction {
	t.Helper()
	tx, err := conn.OpenTransaction(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tx.Rollback)
	return tx
}

func newID(t *testing.T, tx driver.Transaction) uint64 {
	t.Helper()
	id, err := tx.Set("users", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func set(t *testing.T, tx driver.Transaction, id uint64, values driver.DataObject) {
	t.Helper()
	if _, err := tx.Set("users", id, values); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, tx driver.Transaction, id uint64) driver.DataObject {
	t.Helper()
	row, err := tx.Get("users", id)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func commit(t *testing.T, tx driver.Transaction) {
	t.Helper()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionValues(t *testing.T) {
	conn := newConn(t)
	tx := transaction(t, conn)
	alice, bob := newID(t, tx), newID(t, tx)
	if alice != 1 || bob != 2 {
		t.Fatalf("IDs = %d, %d", alice, bob)
	}
	data := map[string]any{"nested": []any{"first", map[string]any{"value": json.Number("3")}}}
	binary := []byte{0, 1, 255}
	birthday := time.Date(2001, time.April, 16, 0, 0, 0, 0, time.UTC)
	set(t, tx, alice, driver.DataObject{
		"name": "Alice", "active": true, "age": int64(16), "balance": "1234567890.123456",
		"birthday": birthday, "ava": driver.NewFile(binary), "friend": bob, "data": data,
	})
	set(t, tx, bob, driver.DataObject{"name": "Bob", "age": int64(18)})
	data["nested"].([]any)[1].(map[string]any)["value"] = json.Number("9")
	binary[0] = 42
	commit(t, tx)

	tx = transaction(t, conn)
	row := get(t, tx, alice)
	avatar, ok := row["ava"].(driver.File)
	if !ok {
		t.Fatalf("avatar type = %T, want driver.File", row["ava"])
	}
	row["ava"] = avatar.Bytes()
	want := driver.DataObject{"name": "Alice", "active": true, "age": int64(16), "balance": "1234567890.123456", "birthday": birthday, "ava": []byte{0, 1, 255}, "friend": bob, "data": map[string]any{"nested": []any{"first", map[string]any{"value": json.Number("3")}}}}
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("row = %#v, want %#v", row, want)
	}
	row["ava"].([]byte)[1] = 99
	row["data"].(map[string]any)["nested"].([]any)[0] = "changed"
	got := get(t, tx, alice)
	gotAvatar, ok := got["ava"].(driver.File)
	if !ok {
		t.Fatalf("avatar type = %T, want driver.File", got["ava"])
	}
	got["ava"] = gotAvatar.Bytes()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored row changed: %#v", got)
	}
	defaults := get(t, tx, bob)
	if defaults["active"] != false || defaults["friend"] != uint64(0) || defaults["balance"] != "0" || defaults["ava"] != nil || defaults["data"] != nil {
		t.Fatalf("defaults = %#v", defaults)
	}
	if row, err := tx.Get("users", 99); row != nil || !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("missing record = %#v, %v", row, err)
	}
	set(t, tx, 99, driver.DataObject{"name": "missing"})
	if err := tx.Delete("users", bob); err != nil {
		t.Fatal(err)
	}
	if row, err := tx.Get("users", bob); row != nil || !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("deleted record = %#v, %v", row, err)
	}
	commit(t, tx)
}

func TestTransactionIncrementAndRollback(t *testing.T) {
	conn := newConn(t)
	tx := transaction(t, conn)
	a, b := newID(t, tx), newID(t, tx)
	set(t, tx, a, driver.DataObject{"balance": "100.012345", "age": int64(2)})
	set(t, tx, b, driver.DataObject{"balance": "0.000000"})
	commit(t, tx)

	tx = transaction(t, conn)
	for range 1000 {
		if err := tx.Increment("users", a, driver.DataObject{"balance": "-0.1"}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Increment("users", b, driver.DataObject{"balance": "0.1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Increment("users", a, driver.DataObject{"age": 3}); err != nil {
		t.Fatal(err)
	}
	if row := get(t, tx, a); row["balance"] != "0.012345" || row["age"] != int64(5) {
		t.Fatalf("Alice = %#v", row)
	}
	if row := get(t, tx, b); row["balance"] != "100.000000" {
		t.Fatalf("Bob = %#v", row)
	}
	commit(t, tx)

	tx = transaction(t, conn)
	set(t, tx, a, driver.DataObject{"name": "rolled back"})
	if err := tx.Delete("users", b); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	tx = transaction(t, conn)
	if get(t, tx, a)["name"] != "" || get(t, tx, b) == nil {
		t.Fatal("rollback changed committed records")
	}
}

func TestFetch(t *testing.T) {
	conn := newConn(t)
	tx := transaction(t, conn)
	for i := 1; i <= 100; i++ {
		id := newID(t, tx)
		set(t, tx, id, driver.DataObject{"name": fmt.Sprintf("user-%03d", i), "age": 10 + i%10})
	}
	commit(t, tx)
	tx = transaction(t, conn)
	for _, tc := range []struct {
		name          string
		reverse       bool
		first, second []uint64
	}{
		{"ascending", false, []uint64{6, 16, 26}, []uint64{36, 46, 56}},
		{"descending", true, []uint64{96, 86, 76}, []uint64{66, 56, 46}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &driver.Query{Table: "users", Limit: 3, Order: []driver.QueryOrder{{Column: "id", Desc: tc.reverse}}}
			q.Filters = append(q.Filters, driver.QueryFilter{Column: "age", Op: driver.OpEqual, Values: []any{16}})
			for _, want := range [][]uint64{tc.first, tc.second} {
				var got []uint64
				var err error
				q.Offset, err = tx.Fetch(q, func(id uint64) error { got = append(got, id); return nil })
				if err != nil || !slices.Equal(got, want) {
					t.Fatalf("Fetch = %v, %v; want %v", got, err, want)
				}
			}
		})
	}
	q := &driver.Query{Table: "users", Index: "by_age", Limit: 3}
	var got []uint64
	_, err := tx.Fetch(q, func(id uint64) error { got = append(got, id); return nil })
	if err != nil || !slices.Equal(got, []uint64{10, 20, 30}) {
		t.Fatalf("indexed Fetch = %v, %v", got, err)
	}
}

func TestFetchInterrupt(t *testing.T) {
	conn := newConn(t)
	tx := transaction(t, conn)
	for range 4 {
		newID(t, tx)
	}
	commit(t, tx)
	tx = transaction(t, conn)
	q := &driver.Query{Table: "users"}
	var got []uint64
	var err error
	q.Offset, err = tx.Fetch(q, func(id uint64) error {
		got = append(got, id)
		if len(got) == 2 {
			return fmt.Errorf("stop: %w", driver.ErrBreak)
		}
		return nil
	})
	if err != nil || q.Offset != "2" || !slices.Equal(got, []uint64{1, 2}) {
		t.Fatalf("interrupted Fetch = %v, %v, %v", got, q.Offset, err)
	}
	got = nil
	_, err = tx.Fetch(q, func(id uint64) error { got = append(got, id); return nil })
	if err != nil || !slices.Equal(got, []uint64{3, 4}) {
		t.Fatalf("resumed Fetch = %v, %v", got, err)
	}
	callbackErr := errors.New("callback error")
	got = nil
	_, err = tx.Fetch(&driver.Query{Table: "users"}, func(id uint64) error { got = append(got, id); return callbackErr })
	if !errors.Is(err, callbackErr) || !slices.Equal(got, []uint64{1}) {
		t.Fatalf("failed Fetch = %v, %v", got, err)
	}
}

func TestSnapshotAndSaveFailure(t *testing.T) {
	initial := &mem.Snapshot{Tables: map[string]map[uint64]driver.DataObject{"users": {5: {"name": "initial", "data": map[string]any{"v": "original"}}}}}
	var saved *mem.Snapshot
	saveErr := errors.New("save failed")
	fail := false
	conn, err := mem.NewConnection(schema(), initial, func(snapshot *mem.Snapshot) error {
		if fail {
			return saveErr
		}
		saved = snapshot
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	initial.Tables["users"][5]["name"] = "changed"
	tx := transaction(t, conn)
	if get(t, tx, 5)["name"] != "initial" {
		t.Fatal("initial snapshot was not copied")
	}
	id := newID(t, tx)
	if id != 6 {
		t.Fatalf("ID = %d, want 6", id)
	}
	set(t, tx, id, driver.DataObject{"name": "saved"})
	commit(t, tx)
	if saved == nil || saved.Sequences["users"] != 6 || saved.Tables["users"][6]["name"] != "saved" {
		t.Fatalf("snapshot = %#v", saved)
	}
	saved.Tables["users"][6]["name"] = "changed"

	fail = true
	tx = transaction(t, conn)
	set(t, tx, id, driver.DataObject{"name": "not committed"})
	if err := tx.Commit(); !errors.Is(err, saveErr) {
		t.Fatalf("Commit error = %v", err)
	}
	tx = transaction(t, conn)
	if got := get(t, tx, id)["name"]; got != "saved" {
		t.Fatalf("name = %v, want saved", got)
	}
	tx.Rollback()

	restored, err := mem.NewConnection(schema(), saved, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	tx = transaction(t, restored)
	if id := newID(t, tx); id != 7 {
		t.Fatalf("restored ID = %d, want 7", id)
	}
}

func TestConcurrentTransactions(t *testing.T) {
	conn := newConn(t)
	tx := transaction(t, conn)
	id := newID(t, tx)
	commit(t, tx)
	const workers, increments = 5, 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range increments {
				tx, err := conn.OpenTransaction(context.Background())
				if err != nil {
					errs <- err
					return
				}
				if err = tx.Increment("users", id, driver.DataObject{"age": 1}); err != nil {
					tx.Rollback()
					errs <- err
					return
				}
				if err = tx.Commit(); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	tx = transaction(t, conn)
	if got := get(t, tx, id)["age"]; got != int64(workers*increments) {
		t.Fatalf("age = %v", got)
	}
}

func TestTransactionContextAndClose(t *testing.T) {
	conn := newConn(t)
	tx := transaction(t, conn)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := conn.OpenTransaction(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenTransaction error = %v", err)
	}
	tx.Rollback()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.OpenTransaction(context.Background()); err == nil {
		t.Fatal("OpenTransaction succeeded after Close")
	}
}
