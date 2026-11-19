// Package mem implements a small, in-memory storage driver. Import this package
// to register the "mem" scheme with driver.NewConnection.
package mem

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/metadata-driven/mdd/driver"
)

// Snapshot is the persisted state used by storage adapters built on mem.
// Row IDs are keys and are not included in the row values.
type Snapshot struct {
	Tables    map[string]map[uint64]driver.DataObject `json:"tables" yaml:"tables"`
	Sequences map[string]uint64                       `json:"sequences" yaml:"sequences"`
}

// SaveFunc persists a complete snapshot before a transaction becomes visible.
// It must not open a transaction on the same connection.
type SaveFunc func(*Snapshot) error

// Connection stores database records in memory.
type Connection struct {
	schema *driver.Database
	gate   chan struct{}
	state  *Snapshot
	save   SaveFunc
	closed bool
	mu     sync.Mutex
}

var _ = driver.Register(func(schema *driver.Database) (driver.Connection, error) {
	return NewConnection(schema, nil, nil)
}, "mem")

// ValidateIndexes rejects indexes over unsupported column types.
func ValidateIndexes(schema *driver.Database) error {
	if schema == nil {
		return nil
	}
	for _, table := range schema.Tables {
		if table == nil {
			return errors.New("invalid table")
		}
		for _, index := range table.Indexes {
			if index == nil {
				continue
			}
			for _, name := range index.Columns {
				for _, col := range table.Columns {
					if col != nil && col.Name == name && col.Type == driver.TypeFile {
						return fmt.Errorf("index %s.%s cannot include file column %q", table.Name, index.Name, name)
					}
					if col != nil && col.Name == name && col.Type == driver.TypeJSON {
						return fmt.Errorf("index %s.%s cannot include JSON column %q", table.Name, index.Name, name)
					}
				}
			}
		}
	}
	return nil
}

// NewConnection creates an in-memory database, optionally initialized from a
// snapshot. save may be supplied by a persistent adapter; it is called on commit.
func NewConnection(schema *driver.Database, initial *Snapshot, save SaveFunc) (*Connection, error) {
	if schema == nil {
		schema = &driver.Database{}
	}
	if err := ValidateIndexes(schema); err != nil {
		return nil, fmt.Errorf("mem: %w", err)
	}
	c := &Connection{schema: schema, gate: make(chan struct{}, 1), save: save}
	c.gate <- struct{}{}
	c.state = copySnapshot(initial)
	for _, table := range schema.Tables {
		if table == nil || table.Name == "" {
			return nil, errors.New("mem: invalid table")
		}
		if c.state.Tables[table.Name] == nil {
			c.state.Tables[table.Name] = map[uint64]driver.DataObject{}
		}
		for id, row := range c.state.Tables[table.Name] {
			if row == nil {
				row = driver.DataObject{}
				c.state.Tables[table.Name][id] = row
			}
			for _, col := range table.Columns {
				value, err := driver.NormalizeColumnValue(col, row[col.Name])
				if err != nil {
					return nil, fmt.Errorf("mem: %s.%s: %w", table.Name, col.Name, err)
				}
				row[col.Name] = value
			}
			if id > c.state.Sequences[table.Name] {
				c.state.Sequences[table.Name] = id
			}
		}
		for _, idx := range table.Indexes {
			if idx == nil || idx.Type != driver.IndexTypeUnique {
				continue
			}
			if len(idx.Columns) == 0 {
				return nil, fmt.Errorf("mem: unique index %s.%s has no columns", table.Name, idx.Name)
			}
			for _, name := range idx.Columns {
				if name == "id" {
					continue
				}
				col := table.ColumnByName(name)
				if col == nil || col.Type == driver.TypeJSON {
					return nil, fmt.Errorf("mem: invalid unique index column %s.%s", table.Name, name)
				}
			}
		}
		for id, row := range c.state.Tables[table.Name] {
			if err := checkUnique(table, c.state.Tables[table.Name], id, row); err != nil {
				return nil, err
			}
		}
	}
	return c, nil
}

// OpenTransaction starts an in-memory transaction controlled by ctx.
func (c *Connection) OpenTransaction(ctx context.Context) (driver.Transaction, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.gate:
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		c.gate <- struct{}{}
		return nil, errors.New("mem: connection is closed")
	}
	if err := ctx.Err(); err != nil {
		c.gate <- struct{}{}
		return nil, err
	}
	return &transaction{conn: c, ctx: ctx, state: copySnapshot(c.state)}, nil
}

// Close closes the in-memory connection.
func (c *Connection) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

type transaction struct {
	conn  *Connection
	ctx   context.Context
	state *Snapshot
	dirty bool
	done  bool
}

func (t *transaction) check() error {
	if t.done {
		return errors.New("mem: transaction is closed")
	}
	return t.ctx.Err()
}
func (t *transaction) finish() { t.done = true; t.conn.gate <- struct{}{} }
func (t *transaction) Commit() error {
	if t.done {
		return errors.New("mem: transaction is closed")
	}
	defer t.finish()
	if err := t.ctx.Err(); err != nil {
		return err
	}
	if t.dirty && t.conn.save != nil {
		if err := t.conn.save(copySnapshot(t.state)); err != nil {
			return err
		}
	}
	if t.dirty {
		t.conn.state = t.state
	}
	return nil
}
func (t *transaction) Rollback() {
	if !t.done {
		t.finish()
	}
}
func (t *transaction) table(name string) (map[uint64]driver.DataObject, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	if t.conn.schema.TableByName(name) == nil {
		return nil, fmt.Errorf("mem: unknown table %q", name)
	}
	return t.state.Tables[name], nil
}
func (t *transaction) Get(table string, id uint64) (driver.DataObject, error) {
	rows, err := t.table(table)
	if err != nil {
		return nil, err
	}
	if row, ok := rows[id]; ok {
		result := copyObject(row)
		for _, col := range t.conn.schema.TableByName(table).Columns {
			value := result[col.Name]
			switch col.Type {
			case driver.TypeJSON:
				if text, ok := value.(string); ok && text == "" {
					result[col.Name] = nil
				}
			case driver.TypeBinary, driver.TypeFile:
				data, err := driver.FileBytes(value)
				if err != nil {
					return nil, fmt.Errorf("mem: %s.%s: %w", table, col.Name, err)
				}
				if len(data) == 0 {
					result[col.Name] = nil
				} else if col.Type == driver.TypeBinary {
					result[col.Name] = data
				} else {
					result[col.Name] = driver.NewFile(data)
				}
			default:
				if !canonicalValue(col.Type, value) {
					result[col.Name] = driver.Cast(value, col.Type, false)
				}
			}
		}
		return result, nil
	}
	return nil, driver.ErrNotFound
}

func canonicalValue(typ driver.DataType, value any) bool {
	switch typ {
	case driver.TypeID:
		_, ok := value.(uint64)
		return ok
	case driver.TypeInteger:
		_, ok := value.(int64)
		return ok
	case driver.TypeBoolean:
		_, ok := value.(bool)
		return ok
	case driver.TypeDecimal, driver.TypeString:
		_, ok := value.(string)
		return ok
	case driver.TypeDatetime:
		v, ok := value.(time.Time)
		return ok && v.Location() == time.UTC
	default:
		return false
	}
}

func (t *transaction) Set(table string, id uint64, values driver.DataObject) (uint64, error) {
	rows, err := t.table(table)
	if err != nil {
		return 0, err
	}
	normalized := make(driver.DataObject, len(values))
	for name, value := range values {
		col := t.conn.schema.TableByName(table).ColumnByName(name)
		if name == "id" || col == nil {
			return 0, fmt.Errorf("mem: unknown or reserved column %q", name)
		}
		value, err = driver.NormalizeColumnValue(col, value)
		if err != nil {
			return 0, fmt.Errorf("mem: %s.%s: %w", table, name, err)
		}
		normalized[name] = value
	}
	if id == 0 {
		if t.state.Sequences[table] == math.MaxUint64 {
			return 0, errors.New("mem: ID sequence exhausted")
		}
		id = t.state.Sequences[table] + 1
	}
	row, ok := rows[id]
	var candidate driver.DataObject
	if !ok {
		candidate = driver.DataObject{}
		for _, col := range t.conn.schema.TableByName(table).Columns {
			candidate[col.Name] = driver.DefaultValue(col.Type)
		}
	} else if hasUnique(t.conn.schema.TableByName(table)) {
		candidate = copyObject(row)
	}
	if hasUnique(t.conn.schema.TableByName(table)) {
		for name, value := range normalized {
			candidate[name] = value
		}
		if err := checkUnique(t.conn.schema.TableByName(table), rows, id, candidate); err != nil {
			return 0, err
		}
	}
	if !ok {
		row = candidate
		rows[id] = candidate
		if t.state.Sequences[table] < id {
			t.state.Sequences[table] = id
		}
		t.dirty = true
	}
	for name, value := range normalized {
		row[name] = clone(value)
	}
	if len(values) > 0 {
		t.dirty = true
	}
	return id, nil
}

func hasUnique(table *driver.Table) bool {
	for _, idx := range table.Indexes {
		if idx != nil && idx.Type == driver.IndexTypeUnique {
			return true
		}
	}
	return false
}

func checkUnique(table *driver.Table, rows map[uint64]driver.DataObject, id uint64, candidate driver.DataObject) error {
	for _, idx := range table.Indexes {
		if idx == nil || idx.Type != driver.IndexTypeUnique {
			continue
		}
		for otherID, row := range rows {
			if otherID == id {
				continue
			}
			equal := true
			for _, name := range idx.Columns {
				if name == "id" {
					equal = false
					break
				}
				col := table.ColumnByName(name)
				comparison, err := compare(candidate[name], row[name], col.Type)
				if err != nil {
					return fmt.Errorf("mem: unique index %s.%s: %w", table.Name, idx.Name, err)
				}
				if comparison != 0 {
					equal = false
					break
				}
			}
			if equal {
				return fmt.Errorf("mem: unique index %s.%s conflicts with record %d", table.Name, idx.Name, otherID)
			}
		}
	}
	return nil
}

func (t *transaction) Delete(table string, id uint64) error {
	rows, err := t.table(table)
	if err != nil {
		return err
	}
	if _, ok := rows[id]; ok {
		delete(rows, id)
		t.dirty = true
	}
	return nil
}

func copySnapshot(src *Snapshot) *Snapshot {
	dst := &Snapshot{Tables: map[string]map[uint64]driver.DataObject{}, Sequences: map[string]uint64{}}
	if src == nil {
		return dst
	}
	for name, seq := range src.Sequences {
		dst.Sequences[name] = seq
	}
	for name, rows := range src.Tables {
		dst.Tables[name] = map[uint64]driver.DataObject{}
		for id, row := range rows {
			dst.Tables[name][id] = copyObject(row)
		}
	}
	return dst
}
func copyObject(obj driver.DataObject) driver.DataObject {
	if obj == nil {
		return nil
	}
	out := make(driver.DataObject, len(obj))
	for k, v := range obj {
		out[k] = clone(v)
	}
	return out
}
func clone(v any) any {
	if v == nil {
		return nil
	}
	return cloneValue(reflect.ValueOf(v)).Interface()
}
func cloneValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		x := reflect.New(v.Type()).Elem()
		x.Set(cloneValue(v.Elem()))
		return x
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		x := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			x.SetMapIndex(it.Key(), cloneValue(it.Value()))
		}
		return x
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		x := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			x.Index(i).Set(cloneValue(v.Index(i)))
		}
		return x
	case reflect.Array:
		x := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			x.Index(i).Set(cloneValue(v.Index(i)))
		}
		return x
	default:
		return v
	}
}
