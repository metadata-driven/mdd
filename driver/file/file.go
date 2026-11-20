// Package file stores records in directories and file fields as separate files.
// Transactions are not atomic: a failed commit can leave some files changed.
// Access from multiple connections or processes to the same directory is unsupported.
package file

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/driver/internal/query"
	"github.com/metadata-driven/mdd/driver/mem"
)

var _ = driver.Register(open, "file")

// Connection stores records and file attributes in a directory.
type Connection struct {
	root   string
	schema *driver.Database
	memory *mem.Connection
}
type recordKey struct {
	table string
	id    uint64
}
type fileKey struct {
	recordKey
	attr string
}
type fileChange struct {
	path   string
	remove bool
}
type transaction struct {
	conn    *Connection
	inner   driver.Transaction
	ctx     context.Context
	temp    string
	records map[recordKey]bool
	files   map[fileKey]fileChange
	done    bool
}

var _ driver.Transaction = (*transaction)(nil)

func safeName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func open(schema *driver.Database) (driver.Connection, error) {
	if schema == nil {
		return nil, errors.New("file: schema required")
	}
	if err := mem.ValidateIndexes(schema); err != nil {
		return nil, fmt.Errorf("file: %w", err)
	}
	u, err := url.Parse(schema.Connection)
	if err != nil {
		return nil, err
	}
	if u.Host != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("file: host, query and fragment are not supported")
	}
	root := u.Path
	if u.Opaque != "" {
		root = u.Opaque
	}
	if root == "" {
		return nil, errors.New("file: directory is required")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, table := range schema.Tables {
		if table == nil || !safeName(table.Name) {
			return nil, errors.New("file: invalid table name")
		}
		for _, col := range table.Columns {
			if col == nil || !safeName(col.Name) || col.Name == "id" {
				return nil, fmt.Errorf("file: invalid column in %s", table.Name)
			}
		}
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	c := &Connection{root: root, schema: schema}
	snapshot, err := c.load()
	if err != nil {
		return nil, err
	}
	c.memory, err = mem.NewConnection(schema, snapshot, nil)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Close closes the connection.
func (c *Connection) Close() error { return c.memory.Close() }

// OpenTransaction starts a transaction controlled by ctx.
func (c *Connection) OpenTransaction(ctx context.Context) (driver.Transaction, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	inner, err := c.memory.OpenTransaction(ctx)
	if err != nil {
		return nil, err
	}
	return &transaction{conn: c, inner: inner, ctx: ctx, records: map[recordKey]bool{}, files: map[fileKey]fileChange{}}, nil
}
func (c *Connection) recordDir(table string, id uint64) string {
	return filepath.Join(c.root, table, strconv.FormatUint(id/1000000, 10), strconv.FormatUint(id/1000%1000, 10), strconv.FormatUint(id, 10))
}
func (c *Connection) load() (*mem.Snapshot, error) {
	snapshot := &mem.Snapshot{Tables: map[string]map[uint64]driver.DataObject{}}
	for _, table := range c.schema.Tables {
		rows := map[uint64]driver.DataObject{}
		snapshot.Tables[table.Name] = rows
		base := filepath.Join(c.root, table.Name)
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) && path == base {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.IsDir() || entry.Name() != "_data.json" {
				return nil
			}
			id, e := strconv.ParseUint(filepath.Base(filepath.Dir(path)), 10, 64)
			if e != nil || id == 0 || filepath.Dir(path) != c.recordDir(table.Name, id) {
				return fmt.Errorf("file: invalid record path %q", path)
			}
			f, e := os.Open(path)
			if e != nil {
				return e
			}
			defer f.Close()
			dec := json.NewDecoder(f)
			dec.UseNumber()
			var obj driver.DataObject
			if e = dec.Decode(&obj); e != nil {
				return fmt.Errorf("file: decode %s: %w", path, e)
			}
			var extra any
			if e = dec.Decode(&extra); e != io.EOF {
				if e == nil {
					return fmt.Errorf("file: multiple JSON values in %s", path)
				}
				return fmt.Errorf("file: decode %s: %w", path, e)
			}
			if obj == nil {
				return fmt.Errorf("file: invalid record %s", path)
			}
			e = nil
			changed := false
			for _, col := range table.Columns {
				if col.Type == driver.TypeFile {
					if v, exists := obj[col.Name]; exists {
						if v != nil {
							return fmt.Errorf("file: file column in %s", path)
						}
						delete(obj, col.Name)
						changed = true
					}
					continue
				}
				if obj[col.Name] == nil {
					obj[col.Name] = driver.DefaultValue(col.Type)
					changed = true
				}
			}
			for _, col := range table.Columns {
				v, ok := obj[col.Name]
				if !ok || v == nil {
					continue
				}
				switch col.Type {
				case driver.TypeFile:
					return fmt.Errorf("file: file column in %s", path)
				case driver.TypeBinary:
					switch value := v.(type) {
					case string:
						obj[col.Name], e = base64.StdEncoding.DecodeString(value)
					case []byte:
						obj[col.Name] = value
					default:
						e = fmt.Errorf("expected base64 string, got %T", v)
					}
				case driver.TypeID:
					obj[col.Name], e = strconv.ParseUint(fmt.Sprint(v), 10, 64)
				case driver.TypeInteger:
					obj[col.Name], e = strconv.ParseInt(fmt.Sprint(v), 10, 64)
				case driver.TypeDecimal:
					if _, ok := v.(string); !ok {
						obj[col.Name] = fmt.Sprint(v)
					}
				case driver.TypeDatetime:
					switch value := v.(type) {
					case string:
						obj[col.Name], e = driver.ParseTime(value)
					case time.Time:
						obj[col.Name] = value.UTC()
					default:
						e = fmt.Errorf("timestamp expected, got %T", v)
					}
				}
				if e != nil {
					return fmt.Errorf("file: %s.%s: %w", table.Name, col.Name, e)
				}
			}
			if changed {
				if err := f.Close(); err != nil {
					return err
				}
				plain := make(driver.DataObject, len(obj))
				for name, value := range obj {
					col := table.ColumnByName(name)
					if col == nil || col.Type != driver.TypeFile {
						plain[name] = value
					}
				}
				data, err := json.MarshalIndent(plain, "", "  ")
				if err != nil {
					return err
				}
				if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
					return err
				}
			}
			rows[id] = obj
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}
func (t *transaction) check() error {
	if t.done {
		return errors.New("file: transaction is closed")
	}
	return t.ctx.Err()
}
func (t *transaction) column(table, attr string) (*driver.Column, error) {
	tab := t.conn.schema.TableByName(table)
	if tab == nil {
		return nil, fmt.Errorf("file: unknown table %q", table)
	}
	col := tab.ColumnByName(attr)
	if col == nil {
		return nil, fmt.Errorf("file: unknown column %s.%s", table, attr)
	}
	return col, nil
}
func (t *transaction) stage(src io.Reader) (string, error) {
	if err := t.check(); err != nil {
		return "", err
	}
	if src == nil {
		return "", errors.New("file: nil reader")
	}
	if t.temp == "" {
		dir, err := os.MkdirTemp(t.conn.root, ".mdd-tx-")
		if err != nil {
			return "", err
		}
		t.temp = dir
	}
	f, err := os.CreateTemp(t.temp, "value-")
	if err != nil {
		return "", err
	}
	path := f.Name()
	_, err = io.Copy(f, &contextReader{ctx: t.ctx, src: src})
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.src.Read(p)
}
func (t *transaction) replaceFile(key fileKey, change fileChange) {
	if prev, ok := t.files[key]; ok && prev.path != "" {
		_ = os.Remove(prev.path)
	}
	t.files[key] = change
	t.records[key.recordKey] = true
}
func (t *transaction) openFile(table string, id uint64, attr string) (io.ReadCloser, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	col, err := t.column(table, attr)
	if err != nil {
		return nil, err
	}
	if col.Type != driver.TypeFile {
		return nil, fmt.Errorf("file: %s.%s is not a file", table, attr)
	}
	row, err := t.inner.Get(table, id)
	if err != nil || row == nil {
		return nil, err
	}
	key := fileKey{recordKey{table, id}, attr}
	path := filepath.Join(t.conn.recordDir(table, id), attr)
	if change, ok := t.files[key]; ok {
		if change.remove {
			return nil, nil
		}
		path = change.path
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return f, err
}
func (t *transaction) Get(table string, id uint64) (driver.DataObject, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	row, err := t.inner.Get(table, id)
	if err != nil || row == nil {
		return row, err
	}
	for _, col := range t.conn.schema.TableByName(table).Columns {
		if col.Type != driver.TypeFile {
			continue
		}
		key := fileKey{recordKey{table, id}, col.Name}
		path := filepath.Join(t.conn.recordDir(table, id), col.Name)
		if change, ok := t.files[key]; ok {
			if change.remove {
				row[col.Name] = nil
				continue
			}
			path = change.path
		}
		if info, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			row[col.Name] = nil
			continue
		} else if err != nil {
			return nil, err
		} else if info.Size() == 0 {
			row[col.Name] = nil
			continue
		}
		name := col.Name
		row[name] = driver.NewFile(func() (io.ReadCloser, error) {
			return t.openFile(table, id, name)
		})
	}
	return row, nil
}
func (t *transaction) Set(table string, id uint64, values driver.DataObject) (uint64, error) {
	if err := t.check(); err != nil {
		return 0, err
	}
	tab := t.conn.schema.TableByName(table)
	if tab == nil {
		return 0, fmt.Errorf("file: unknown table %q", table)
	}
	plain := driver.DataObject{}
	pending := map[string]fileChange{}
	cleanup := func() {
		for _, v := range pending {
			if v.path != "" {
				_ = os.Remove(v.path)
			}
		}
	}
	for name, v := range values {
		col := tab.ColumnByName(name)
		if col == nil {
			cleanup()
			return 0, fmt.Errorf("file: unknown column %s.%s", table, name)
		}
		if col.Type != driver.TypeFile {
			plain[name] = v
			continue
		}
		if v == nil {
			pending[name] = fileChange{remove: true}
			continue
		}
		var r io.Reader
		switch x := v.(type) {
		case []byte:
			if x == nil {
				pending[name] = fileChange{remove: true}
				continue
			}
			r = bytes.NewReader(x)
		case string:
			r = strings.NewReader(x)
		case io.Reader:
			r = x
		default:
			cleanup()
			return 0, fmt.Errorf("file: invalid file value for %s.%s", table, name)
		}
		path, e := t.stage(r)
		if e != nil {
			cleanup()
			return 0, e
		}
		pending[name] = fileChange{path: path}
	}
	id, err := t.inner.Set(table, id, plain)
	if err != nil {
		cleanup()
		return 0, err
	}
	t.records[recordKey{table, id}] = true
	for name, change := range pending {
		t.replaceFile(fileKey{recordKey{table, id}, name}, change)
	}
	return id, nil
}
func (t *transaction) Increment(table string, id uint64, values driver.DataObject) error {
	if err := t.check(); err != nil {
		return err
	}
	row, err := t.inner.Get(table, id)
	if errors.Is(err, driver.ErrNotFound) {
		return nil
	}
	if err != nil || row == nil {
		return err
	}
	if err = t.inner.Increment(table, id, values); err == nil && len(values) > 0 {
		t.records[recordKey{table, id}] = true
	}
	return err
}
func (t *transaction) Delete(table string, id uint64) error {
	if err := t.check(); err != nil {
		return err
	}
	row, err := t.inner.Get(table, id)
	if errors.Is(err, driver.ErrNotFound) {
		return nil
	}
	if err != nil || row == nil {
		return err
	}
	if err = t.inner.Delete(table, id); err != nil {
		return err
	}
	key := recordKey{table, id}
	t.records[key] = false
	for b, v := range t.files {
		if b.recordKey == key {
			if v.path != "" {
				_ = os.Remove(v.path)
			}
			delete(t.files, b)
		}
	}
	return nil
}
func (t *transaction) Fetch(q *driver.Query, fn func(uint64) error) (string, error) {
	if err := t.check(); err != nil {
		return "", err
	}
	if q != nil {
		plan, err := query.Resolve(t.conn.schema, q)
		if err != nil {
			return "", fmt.Errorf("file: %w", err)
		}
		index := plan.Index
		columns := append([]driver.QueryOrder(nil), q.Order...)
		for _, filter := range q.Filters {
			columns = append(columns, driver.QueryOrder{Source: filter.Source, Column: filter.Column})
		}
		if index != nil && len(q.Order) == 0 {
			for _, name := range index.Columns {
				columns = append(columns, driver.QueryOrder{Column: name})
			}
		}
		for _, item := range columns {
			col, err := plan.Column(item.Source, item.Column)
			if err != nil {
				return "", fmt.Errorf("file: %w", err)
			}
			if col.Type == driver.TypeFile {
				return "", fmt.Errorf("file: file field %q in source %q cannot be queried", item.Column, item.Source)
			}
		}
	}
	return t.inner.Fetch(q, fn)
}
func (t *transaction) Rollback() {
	if t.done {
		return
	}
	t.done = true
	t.inner.Rollback()
	if t.temp != "" {
		_ = os.RemoveAll(t.temp)
	}
}
func (t *transaction) Commit() error {
	if err := t.check(); err != nil {
		t.Rollback()
		return err
	}
	fail := func(err error) error {
		_ = t.conn.Close()
		t.Rollback()
		return fmt.Errorf("file: commit failed; reopen the storage: %w", err)
	}
	keys := make([]recordKey, 0, len(t.records))
	for key := range t.records {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].table != keys[j].table {
			return keys[i].table < keys[j].table
		}
		return keys[i].id < keys[j].id
	})
	var table *driver.Table
	for _, key := range keys {
		dir := t.conn.recordDir(key.table, key.id)
		if !t.records[key] {
			if err := os.RemoveAll(dir); err != nil {
				return fail(err)
			}
			continue
		}
		row, err := t.inner.Get(key.table, key.id)
		if err != nil {
			return fail(err)
		}
		if table == nil || table.Name != key.table {
			table = t.conn.schema.TableByName(key.table)
		}
		for _, col := range table.Columns {
			if col.Type == driver.TypeFile {
				delete(row, col.Name)
			} else if row[col.Name] == nil {
				row[col.Name] = driver.DefaultValue(col.Type)
			}
		}
		data, err := json.MarshalIndent(row, "", "  ")
		if err != nil {
			return fail(fmt.Errorf("encode %s/%d: %w", key.table, key.id, err))
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			return fail(err)
		}
		for fileKey, change := range t.files {
			if fileKey.recordKey != key {
				continue
			}
			dest := filepath.Join(dir, fileKey.attr)
			if change.remove {
				if err = os.Remove(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fail(err)
				}
			} else if err = os.Rename(change.path, dest); err != nil {
				return fail(err)
			}
		}
		if err = os.WriteFile(filepath.Join(dir, "_data.json"), append(data, '\n'), 0600); err != nil {
			return fail(err)
		}
	}
	if err := t.inner.Commit(); err != nil {
		return fail(err)
	}
	t.done = true
	if t.temp != "" {
		_ = os.RemoveAll(t.temp)
	}
	return nil
}
