package mysql

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	mathrand "math/rand"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goldic/js"
	"github.com/metadata-driven/mdd/driver"
)

type testConnection struct {
	*driver.Database
	driver.Connection
}

func newTestConn(t *testing.T) *testConnection {
	t.Helper()
	const dsn = "mysql://root@/testdb"
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	table := fmt.Sprintf("mdd_test_%x", suffix)
	db, err := sql.Open("mysql", strings.TrimPrefix(dsn, "mysql://"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE `" + table + "` (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY)"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	conn := &testConnection{}
	t.Cleanup(func() {
		if conn.Connection != nil {
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
		}
		if _, err := db.Exec("DROP TABLE `" + table + "`"); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	conn.Database = &driver.Database{

		Connection: dsn,

		Tables: []*driver.Table{{
			Name:    table,
			Comment: "(id:1000) Users table",

			Columns: []*driver.Column{
				{Name: "name", Type: driver.TypeString, Length: 255},
				{Name: "active", Type: driver.TypeBoolean},
				{Name: "age", Type: driver.TypeInteger},
				{Name: "balance", Type: driver.TypeDecimal, Precision: 20, Scale: 6},
				{Name: "birthday", Type: driver.TypeDatetime},
				{Name: "ava", Type: driver.TypeFile},
				{Name: "friend", Type: driver.TypeID},
				{Name: "bio", Type: driver.TypeString},
				{Name: "data", Type: driver.TypeJSON},
			},
			Indexes: []*driver.Index{
				{Type: driver.IndexTypeIndex, Name: "idx_111", Columns: []string{"name", "age"}},
				{Type: driver.IndexTypeIndex, Name: "idx_222", Columns: []string{"active", "balance"}},
			},
		}},
	}
	conn.Connection, err = driver.NewConnection(conn.Database)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestNewConnection(t *testing.T) {
	var conn = newTestConn(t)
	var tab = conn.Tables[0].Name
	var idA, idB uint64
	birthday := time.Date(2001, time.April, 16, 0, 0, 0, 123456000, time.UTC)

	err := conn.exec(func(tx driver.Transaction) {

		idA = txNewID(tx, tab)
		idB = txNewID(tx, tab)
		txSet(tx, tab, idA, driver.DataObject{
			"name":     "Alice",
			"active":   true,
			"age":      int64(16),
			"balance":  "1234567890.123456",
			"birthday": birthday,
			"ava":      driver.NewFile("Alice`s icon 😁"),
			"data":     js.Object{"a": json.Number("1"), "b": json.Number("2")},
		})
		txSet(tx, tab, idB, driver.DataObject{
			"name": "Bob",
			"age":  int64(18),
		})
		txSet(tx, tab, idA, driver.DataObject{"friend": idB})
		require(t, idA > 0, "idA = %d, want a positive ID", idA)
		require(t, idB > idA, "idB = %d, want greater than %d", idB, idA)
	})
	require(t, err == nil, "transaction error: %v", err)

	//-------------
	err = conn.exec(func(tx driver.Transaction) {

		a := txGet(tx, tab, idA)

		assertEqual(t, js.ToStr(a["name"]), "Alice")
		assertEqual(t, js.ToBool(a["active"]), true)
		assertEqual(t, js.ToInt(a["age"]), 16)
		assertEqual(t, js.ToNum(a["balance"]), 1234567890.123456)
		assertEqual(t, a["birthday"], any(birthday))
		assertEqual(t, js.ToUint64(a["friend"]), idB)
		assertEqual(t, js.ToStr(a["ava"]), "Alice`s icon 😁")
		assertEqual(t, js.NewObject(a["data"]).String(), `{"a":1,"b":2}`)

		b := txGet(tx, tab, idB)

		assertEqual(t, js.ToStr(b["name"]), "Bob")
		assertEqual(t, js.ToBool(b["active"]), false)
		assertEqual(t, js.ToInt(b["age"]), 18)
		assertEqual(t, b["birthday"], any(time.Unix(0, 0).UTC()))
		assertEqual(t, js.ToUint64(b["friend"]), uint64(0))
		assertEqual(t, js.ToStr(b["ava"]), "")

	})
	require(t, err == nil, "transaction error: %v", err)
}

func TestTransaction_Inc(t *testing.T) {
	var conn = newTestConn(t)
	var tab = conn.Database.Tables[0].Name
	var idA, idB uint64

	err := conn.exec(func(tx driver.Transaction) {
		idA = txNewID(tx, tab)
		idB = txNewID(tx, tab)
		txSet(tx, tab, idA, driver.DataObject{
			"balance": +100.012345,
		})
		txSet(tx, tab, idB, driver.DataObject{
			"balance": 0,
		})
	})
	require(t, err == nil, "transaction error: %v", err)

	//-----------------------
	const N = 1666
	var countOK, countErr int64

	runConcurrency(N, func() {
		err := conn.exec(func(tx driver.Transaction) {
			txIncrement := func(id uint64, v driver.DataObject) {
				t.Helper()
				if err := tx.Increment(tab, id, v); err != nil {
					t.Errorf("Increment() error = %v", err)
					log.Panic(err)
				}
			}

			const delta = 0.1

			txIncrement(idA, driver.DataObject{"balance": -delta})
			txIncrement(idB, driver.DataObject{"balance": +delta})

			if bal := txGetVal(tx, tab, idA, "balance"); js.ToNum(bal) < 0 {
				panic("not enough funds")
			}
		})
		if err == nil {
			atomic.AddInt64(&countOK, 1)
		} else {
			atomic.AddInt64(&countErr, 1)
		}
	})
	assertEqual(t, countOK, int64(1000))
	assertEqual(t, countErr, int64(666))

	//-----------------
	err = conn.exec(func(tx driver.Transaction) {
		balA := txGetVal(tx, tab, idA, "balance")
		balB := txGetVal(tx, tab, idB, "balance")

		assertEqual(t, js.ToNum(balA), 0.012345)
		assertEqual(t, js.ToNum(balB), 100.0000)
	})
	require(t, err == nil, "transaction error: %v", err)
}

func TestTransaction_Fetch(t *testing.T) {
	var conn = newTestConn(t)
	var tab = conn.Database.Tables[0].Name
	var rnd = mathrand.New(mathrand.NewSource(0))

	err := conn.exec(func(tx driver.Transaction) {
		for i := 1; i <= 1000; i++ {
			id := txNewID(tx, tab)
			txSet(tx, tab, id, driver.DataObject{
				"age":     10 + i%10,
				"name":    fmt.Sprintf("user-%05d", i),
				"balance": fmt.Sprintf("%.6f", rnd.Float64()*1e6),
			})
		}
	})
	require(t, err == nil, "transaction error: %v", err)

	//--------- fetch data
	err = conn.exec(func(tx driver.Transaction) {
		q := &driver.Query{
			Table: tab,
			Limit: 10,
		}
		q.Filters = append(q.Filters, driver.QueryFilter{Column: "age", Op: "=", Values: []any{16}})

		//----- query-1
		res := make([]uint64, 0)
		q.Offset, err = tx.Fetch(q, func(id uint64) error {
			res = append(res, id)
			return nil
		})
		require(t, err == nil, "Fetch() error = %v", err)
		require(t, slices.Equal(res, []uint64{6, 16, 26, 36, 46, 56, 66, 76, 86, 96}), "got %v, want %v", res, []uint64{6, 16, 26, 36, 46, 56, 66, 76, 86, 96})

		//----- query-2
		res = make([]uint64, 0)
		q.Offset, err = tx.Fetch(q, func(id uint64) error {
			res = append(res, id)
			return nil
		})
		require(t, err == nil, "Fetch() error = %v", err)
		require(t, slices.Equal(res, []uint64{106, 116, 126, 136, 146, 156, 166, 176, 186, 196}), "got %v, want %v", res, []uint64{106, 116, 126, 136, 146, 156, 166, 176, 186, 196})
	})
	require(t, err == nil, "transaction error: %v", err)

	//--------- fetch data with desc order
	err = conn.exec(func(tx driver.Transaction) {
		q := &driver.Query{
			Table: tab,
			Order: []driver.QueryOrder{{Column: "id", Desc: true}},
			Limit: 10,
		}
		q.Filters = append(q.Filters, driver.QueryFilter{Column: "age", Op: "=", Values: []any{16}})

		//----- query-1
		res := make([]uint64, 0)
		q.Offset, err = tx.Fetch(q, func(id uint64) error {
			res = append(res, id)
			return nil
		})
		require(t, err == nil, "Fetch() error = %v", err)
		require(t, slices.Equal(res, []uint64{996, 986, 976, 966, 956, 946, 936, 926, 916, 906}), "got %v, want %v", res, []uint64{996, 986, 976, 966, 956, 946, 936, 926, 916, 906})

		//----- query-2
		res = make([]uint64, 0)
		q.Offset, err = tx.Fetch(q, func(id uint64) error {
			res = append(res, id)
			return nil
		})
		require(t, err == nil, "Fetch() error = %v", err)
		require(t, slices.Equal(res, []uint64{896, 886, 876, 866, 856, 846, 836, 826, 816, 806}), "got %v, want %v", res, []uint64{896, 886, 876, 866, 856, 846, 836, 826, 816, 806})
	})
	require(t, err == nil, "transaction error: %v", err)
}

func TestTransaction_FetchInterrupt(t *testing.T) {
	conn := newTestConn(t)
	tab := conn.Database.Tables[0].Name

	err := conn.exec(func(tx driver.Transaction) {
		for range 4 {
			txNewID(tx, tab)
		}
	})
	require(t, err == nil, "transaction error: %v", err)

	err = conn.exec(func(tx driver.Transaction) {
		q := &driver.Query{Table: tab}
		var ids []uint64
		var fetchErr error
		q.Offset, fetchErr = tx.Fetch(q, func(id uint64) error {
			ids = append(ids, id)
			if len(ids) == 2 {
				return fmt.Errorf("stop: %w", driver.ErrBreak)
			}
			return nil
		})
		require(t, fetchErr == nil, "Fetch() error = %v", fetchErr)
		require(t, slices.Equal(ids, []uint64{1, 2}), "got ids %v", ids)
		assertEqual(t, q.Offset, "2")

		ids = nil
		_, fetchErr = tx.Fetch(q, func(id uint64) error {
			ids = append(ids, id)
			return nil
		})
		require(t, fetchErr == nil, "Fetch() error = %v", fetchErr)
		require(t, slices.Equal(ids, []uint64{3, 4}), "got ids %v", ids)

		callbackErr := errors.New("callback error")
		ids = nil
		_, fetchErr = tx.Fetch(&driver.Query{Table: tab}, func(id uint64) error {
			ids = append(ids, id)
			return callbackErr
		})
		require(t, errors.Is(fetchErr, callbackErr), "Fetch() error = %v", fetchErr)
		require(t, slices.Equal(ids, []uint64{1}), "got ids %v", ids)
	})
	require(t, err == nil, "transaction error: %v", err)
}

func txNewID(tx driver.Transaction, tab string) uint64 {
	id, err := tx.Set(tab, 0, nil)
	if err != nil {
		log.Panic(err)
	}
	return id
}

func txSet(tx driver.Transaction, tab string, id uint64, v driver.DataObject) {
	if _, err := tx.Set(tab, id, v); err != nil {
		log.Panic(err)
	}
}

func txGet(tx driver.Transaction, tab string, id uint64) driver.DataObject {
	v, err := tx.Get(tab, id)
	if err != nil {
		log.Panic(err)
	}
	return v
}

func txGetVal(tx driver.Transaction, tab string, id uint64, name string) any {
	v := txGet(tx, tab, id)
	return v[name]
}

func runConcurrency(n int64, fn func()) {
	r := n
	const maxRoutines = 20
	if r > maxRoutines {
		r = maxRoutines
	}
	var wg sync.WaitGroup
	for ; r > 0; r-- {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for atomic.AddInt64(&n, -1) >= 0 {
				fn()
			}
		}()
	}
	wg.Wait()
}

func (c *testConnection) exec(fn func(tx driver.Transaction)) (err error) {
	tx, err := c.Connection.OpenTransaction(nil)
	if err != nil {
		return
	}

	committed := false
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tx-ERR: %v", r)
		}
		if !committed {
			tx.Rollback()
		}
	}()

	fn(tx)

	err = tx.Commit()
	committed = true
	return
}

func require(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	require(t, got == want, "got %v, want %v", got, want)
}
