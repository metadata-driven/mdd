// Package sqlite implements a SQLite storage driver without CGO.
package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/driver/internal/query"
	sqlite "modernc.org/sqlite"
)

var memoryID atomic.Uint64

func decodeJSON(data string) (any, error) {
	if data == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("mdd_decimal_add", 2, func(_ *sqlite.FunctionContext, v []sqldriver.Value) (sqldriver.Value, error) {
		a, ok := new(big.Int).SetString(fmt.Sprint(v[0]), 10)
		if !ok {
			return nil, errors.New("invalid decimal")
		}
		b, ok := new(big.Int).SetString(fmt.Sprint(v[1]), 10)
		if !ok {
			return nil, errors.New("invalid decimal delta")
		}
		width := len(fmt.Sprint(v[0]))
		limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(width-1)), nil)
		a.Add(a, b)
		if a.Sign() <= 0 || a.Cmp(new(big.Int).Mul(limit, big.NewInt(2))) >= 0 {
			return nil, errors.New("decimal overflow")
		}
		return fmt.Sprintf("%0*s", width, a.String()), nil
	})
	_ = driver.Register(open, "sqlite", "sqlite3", "db")
}

type Connection struct {
	schema *driver.Database
	db     *sql.DB
}

func open(schema *driver.Database) (driver.Connection, error) {
	if schema == nil {
		return nil, errors.New("sqlite: schema required")
	}
	u, err := url.Parse(schema.Connection)
	if err != nil {
		return nil, err
	}
	if u.Host != "" {
		return nil, errors.New("sqlite: host is not supported")
	}
	path := u.Path
	if u.Opaque != "" {
		path = u.Opaque
	}
	if path == "" {
		return nil, errors.New("sqlite: path required")
	}
	dsn := (&url.URL{Scheme: "file", Path: path}).String()
	if path == ":memory:" {
		dsn = fmt.Sprintf("file:mdd_mem_%d?mode=memory&cache=shared", memoryID.Add(1))
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	dsn += separator + "_pragma=busy_timeout%285000%29"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	c := &Connection{schema: schema, db: db}
	if _, err = db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, lockError(err)
	}
	if err = c.rebase(); err != nil {
		db.Close()
		return nil, err
	}
	return c, nil
}
func (c *Connection) Close() error { return c.db.Close() }
func (c *Connection) OpenTransaction(ctx context.Context) (driver.Transaction, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, lockError(err)
	}
	return &transaction{c: c, tx: tx, ctx: ctx}, nil
}
func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func lockError(err error) error {
	if err == nil {
		return nil
	}
	var e *sqlite.Error
	if errors.As(err, &e) && (e.Code()&255 == 5 || e.Code()&255 == 6) {
		return fmt.Errorf("%w: %v", driver.ErrLock, err)
	}
	return err
}
func columnType(c *driver.Column) (string, error) {
	switch c.Type {
	case driver.TypeID, driver.TypeBoolean, driver.TypeInteger:
		return "INTEGER NOT NULL DEFAULT 0", nil
	case driver.TypeDecimal:
		precision := decimalPrecision(c)
		if precision < 1 || precision > 65 || c.Scale < 0 || c.Scale > precision {
			return "", fmt.Errorf("sqlite: invalid decimal precision or scale for %s", c.Name)
		}
		z, err := encodeDecimal("0", c)
		if err != nil {
			return "", err
		}
		return "TEXT NOT NULL DEFAULT '" + z + "'", nil
	case driver.TypeString:
		return "TEXT NOT NULL DEFAULT ''", nil
	case driver.TypeDatetime:
		return "TEXT NOT NULL DEFAULT '1970-01-01 00:00:00.000000'", nil
	case driver.TypeBinary, driver.TypeFile:
		return "BLOB NOT NULL DEFAULT X''", nil
	case driver.TypeJSON:
		return "TEXT NOT NULL DEFAULT ''", nil
	default:
		return "", fmt.Errorf("sqlite: unknown column type %d", c.Type)
	}
}

func decimalPrecision(col *driver.Column) int {
	if col.Precision == 0 {
		return 38
	}
	return col.Precision
}
func (c *Connection) rebase() error {
	ctx := context.Background()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return lockError(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS "_mdd_sqlite_columns" (table_name TEXT NOT NULL, column_name TEXT NOT NULL, definition TEXT NOT NULL, PRIMARY KEY (table_name, column_name))`); err != nil {
		return lockError(err)
	}
	for _, table := range c.schema.Tables {
		if table == nil || table.Name == "" || strings.EqualFold(table.Name, "_mdd_sqlite_columns") {
			return errors.New("sqlite: invalid table")
		}
		_, err = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+quote(table.Name)+" (id INTEGER PRIMARY KEY AUTOINCREMENT)")
		if err != nil {
			return lockError(err)
		}
		rows, e := tx.QueryContext(ctx, "PRAGMA table_info("+quote(table.Name)+")")
		if e != nil {
			return e
		}
		cols := map[string]string{}
		nullable := false
		for rows.Next() {
			var cid, nn, pk int
			var name, typ string
			var def any
			if e = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); e != nil {
				rows.Close()
				return e
			}
			cols[name] = strings.ToUpper(typ)
			if name != "id" && nn == 0 {
				nullable = true
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, col := range table.Columns {
			if col == nil || col.Name == "" || strings.EqualFold(col.Name, "id") {
				return fmt.Errorf("sqlite: invalid or reserved column")
			}
			def, e := columnType(col)
			if e != nil {
				return e
			}
			expected := strings.Fields(def)[0]
			description := fmt.Sprintf("%d:%d:%d", col.Type, col.Precision, col.Scale)
			var previous string
			e = tx.QueryRowContext(ctx, `SELECT definition FROM "_mdd_sqlite_columns" WHERE table_name=? AND column_name=?`, table.Name, col.Name).Scan(&previous)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return lockError(e)
			}
			if e == nil && previous != description {
				return fmt.Errorf("sqlite: incompatible schema change for %s.%s", table.Name, col.Name)
			}
			if errors.Is(e, sql.ErrNoRows) {
				if _, exists := cols[col.Name]; exists && col.Type == driver.TypeDecimal {
					return fmt.Errorf("sqlite: cannot verify decimal format for existing column %s.%s", table.Name, col.Name)
				}
				if _, e = tx.ExecContext(ctx, `INSERT INTO "_mdd_sqlite_columns" (table_name,column_name,definition) VALUES (?,?,?)`, table.Name, col.Name, description); e != nil {
					return lockError(e)
				}
			}
			if typ, ok := cols[col.Name]; ok {
				if typ != expected {
					return fmt.Errorf("sqlite: column %s.%s has type %s, expected %s", table.Name, col.Name, typ, expected)
				}
				continue
			}
			if _, e = tx.ExecContext(ctx, "ALTER TABLE "+quote(table.Name)+" ADD COLUMN "+quote(col.Name)+" "+def); e != nil {
				return lockError(e)
			}
			cols[col.Name] = expected
		}
		if nullable {
			if err = migrateNotNull(ctx, tx, table, cols); err != nil {
				return err
			}
		}
		for _, col := range table.Columns {
			if col.Type == driver.TypeJSON {
				if _, err = tx.ExecContext(ctx, "UPDATE "+quote(table.Name)+" SET "+quote(col.Name)+"='' WHERE "+quote(col.Name)+"='null'"); err != nil {
					return lockError(err)
				}
			}
		}
		indexPrefix := fmt.Sprintf("_mdd_%d_%s_", len(table.Name), table.Name)
		expectedIndexes := make(map[string]bool, len(table.Indexes))
		for _, idx := range table.Indexes {
			if idx != nil {
				expectedIndexes[indexPrefix+idx.Name] = true
			}
		}
		indexList, e := tx.QueryContext(ctx, "SELECT name FROM pragma_index_list(?)", table.Name)
		if e != nil {
			return lockError(e)
		}
		var obsolete []string
		for indexList.Next() {
			var name string
			if e = indexList.Scan(&name); e != nil {
				indexList.Close()
				return e
			}
			if strings.HasPrefix(name, indexPrefix) && !expectedIndexes[name] {
				obsolete = append(obsolete, name)
			}
		}
		e = indexList.Err()
		indexList.Close()
		if e != nil {
			return e
		}
		for _, name := range obsolete {
			if _, e = tx.ExecContext(ctx, "DROP INDEX "+quote(name)); e != nil {
				return lockError(e)
			}
		}
		for _, idx := range table.Indexes {
			if idx == nil || idx.Name == "" || len(idx.Columns) == 0 {
				return errors.New("sqlite: invalid index")
			}
			if idx.Type != "" && idx.Type != driver.IndexTypeIndex && idx.Type != driver.IndexTypeUnique {
				return fmt.Errorf("sqlite: unsupported index type %q", idx.Type)
			}
			names := make([]string, len(idx.Columns))
			for i, name := range idx.Columns {
				if name != "id" && table.ColumnByName(name) == nil {
					return fmt.Errorf("sqlite: unknown index column %q", name)
				}
				names[i] = quote(name)
			}
			// SQLite index names are database-wide.
			indexName := indexPrefix + idx.Name
			var actualUnique int
			e = tx.QueryRowContext(ctx, `SELECT "unique" FROM pragma_index_list(?) WHERE name=?`, table.Name, indexName).Scan(&actualUnique)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return lockError(e)
			}
			indexRows, e := tx.QueryContext(ctx, "PRAGMA index_info("+quote(indexName)+")")
			if e != nil {
				return lockError(e)
			}
			var actual []string
			for indexRows.Next() {
				var seq, cid int
				var name string
				if e = indexRows.Scan(&seq, &cid, &name); e != nil {
					indexRows.Close()
					return e
				}
				actual = append(actual, name)
			}
			e = indexRows.Err()
			indexRows.Close()
			if e != nil {
				return e
			}
			if len(actual) > 0 && (!slices.Equal(actual, idx.Columns) || (actualUnique != 0) != (idx.Type == driver.IndexTypeUnique)) {
				if _, e = tx.ExecContext(ctx, "DROP INDEX "+quote(indexName)); e != nil {
					return lockError(e)
				}
			}
			unique := ""
			if idx.Type == driver.IndexTypeUnique {
				unique = "UNIQUE "
			}
			if _, err = tx.ExecContext(ctx, "CREATE "+unique+"INDEX IF NOT EXISTS "+quote(indexName)+" ON "+quote(table.Name)+" ("+strings.Join(names, ",")+")"); err != nil {
				return lockError(err)
			}
		}
	}
	return lockError(tx.Commit())
}

var unsupportedTableConstraint = regexp.MustCompile(`(?i)\b(CHECK|COLLATE|UNIQUE|REFERENCES|GENERATED|WITHOUT|STRICT)\b`)

// migrateNotNull rebuilds an ordinary managed table in the same transaction.
func migrateNotNull(ctx context.Context, tx *sql.Tx, table *driver.Table, existing map[string]string) error {
	if len(existing) != len(table.Columns)+1 {
		return fmt.Errorf("sqlite: cannot migrate %s with unmanaged columns", table.Name)
	}
	var definition string
	if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type='table' AND name=?", table.Name).Scan(&definition); err != nil {
		return err
	}
	if !strings.Contains(strings.ToUpper(definition), "AUTOINCREMENT") || unsupportedTableConstraint.MatchString(definition) {
		return fmt.Errorf("sqlite: cannot safely migrate custom table %s", table.Name)
	}
	rows, err := tx.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, name := range tables {
		rows, err = tx.QueryContext(ctx, "PRAGMA foreign_key_list("+quote(name)+")")
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, seq int
			var parent, from, to, onUpdate, onDelete, match string
			if err = rows.Scan(&id, &seq, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				rows.Close()
				return err
			}
			if name == table.Name || strings.EqualFold(parent, table.Name) {
				rows.Close()
				return fmt.Errorf("sqlite: cannot automatically migrate foreign keys for %s", table.Name)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	rows, err = tx.QueryContext(ctx, "SELECT type, sql FROM sqlite_master WHERE tbl_name=? AND type IN ('index','trigger')", table.Name)
	if err != nil {
		return err
	}
	var objects []string
	for rows.Next() {
		var kind string
		var sqlText sql.NullString
		if err = rows.Scan(&kind, &sqlText); err != nil {
			rows.Close()
			return err
		}
		if !sqlText.Valid {
			rows.Close()
			return fmt.Errorf("sqlite: cannot automatically migrate constraint on %s", table.Name)
		}
		objects = append(objects, sqlText.String)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var seq sql.NullInt64
	err = tx.QueryRowContext(ctx, "SELECT seq FROM sqlite_sequence WHERE name=?", table.Name).Scan(&seq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	temp := fmt.Sprintf("_mdd_migrate_%d", memoryID.Add(1))
	defs := []string{"id INTEGER PRIMARY KEY AUTOINCREMENT"}
	names := []string{"id"}
	values := []string{"id"}
	for _, col := range table.Columns {
		def, err := columnType(col)
		if err != nil {
			return err
		}
		name := quote(col.Name)
		defs = append(defs, name+" "+def)
		names = append(names, name)
		defaultSQL := strings.SplitN(def, " DEFAULT ", 2)[1]
		value := "COALESCE(" + name + "," + defaultSQL + ")"
		if col.Type == driver.TypeJSON {
			value = "CASE WHEN " + name + " IS NULL OR " + name + "='null' THEN '' ELSE " + name + " END"
		}
		values = append(values, value)
	}
	for _, query := range []string{
		"CREATE TABLE " + quote(temp) + " (" + strings.Join(defs, ",") + ")",
		"INSERT INTO " + quote(temp) + " (" + strings.Join(names, ",") + ") SELECT " + strings.Join(values, ",") + " FROM " + quote(table.Name),
		"DROP TABLE " + quote(table.Name),
		"ALTER TABLE " + quote(temp) + " RENAME TO " + quote(table.Name),
	} {
		if _, err = tx.ExecContext(ctx, query); err != nil {
			return lockError(err)
		}
	}
	if seq.Valid {
		if _, err = tx.ExecContext(ctx, "UPDATE sqlite_sequence SET seq=MAX(seq,?) WHERE name=?", seq.Int64, table.Name); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO sqlite_sequence(name,seq) SELECT ?,? WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name=?)", table.Name, seq.Int64, table.Name); err != nil {
			return err
		}
	}
	for _, query := range objects {
		if _, err = tx.ExecContext(ctx, query); err != nil {
			return lockError(err)
		}
	}
	return nil
}

func decimalScaled(v any, col *driver.Column) (*big.Int, error) {
	text, ok := v.(string)
	if !ok {
		text = fmt.Sprint(v)
	}
	n, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, fmt.Errorf("sqlite: invalid decimal %v", v)
	}
	mul := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(col.Scale)), nil)
	n.Mul(n, new(big.Rat).SetInt(mul))
	if !n.IsInt() {
		return nil, fmt.Errorf("sqlite: decimal exceeds scale %d", col.Scale)
	}
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimalPrecision(col))), nil)
	if new(big.Int).Abs(n.Num()).Cmp(limit) >= 0 {
		return nil, errors.New("sqlite: decimal overflow")
	}
	return new(big.Int).Set(n.Num()), nil
}
func encodeDecimal(v any, col *driver.Column) (string, error) {
	n, err := decimalScaled(v, col)
	if err != nil {
		return "", err
	}
	base := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimalPrecision(col))), nil)
	n.Add(n, base)
	return fmt.Sprintf("%0*s", decimalPrecision(col)+1, n.String()), nil
}
func decodeDecimal(v string, col *driver.Column) (string, error) {
	n, ok := new(big.Int).SetString(v, 10)
	if !ok {
		return "", errors.New("sqlite: invalid decimal encoding")
	}
	base := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimalPrecision(col))), nil)
	n.Sub(n, base)
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(col.Scale)), nil)
	return new(big.Rat).SetFrac(n, den).FloatString(col.Scale), nil
}
func argument(v any, col *driver.Column) (any, error) {
	if col.Type == driver.TypeJSON {
		return driver.JSONText(v)
	}
	v, err := driver.NormalizeColumnValue(col, v)
	if err != nil {
		return nil, err
	}
	switch col.Type {
	case driver.TypeDecimal:
		return encodeDecimal(v, col)
	case driver.TypeDatetime:
		if value, ok := v.(time.Time); ok {
			return value.Format("2006-01-02 15:04:05.000000"), nil
		}
	case driver.TypeID:
		if id, ok := v.(uint64); ok && id > math.MaxInt64 {
			return nil, errors.New("sqlite: ID exceeds signed 64-bit range")
		}
	}
	return v, nil
}

type transaction struct {
	c   *Connection
	tx  *sql.Tx
	ctx context.Context
}

func (t *transaction) Commit() error { return lockError(t.tx.Commit()) }
func (t *transaction) Rollback()     { _ = t.tx.Rollback() }
func (t *transaction) table(name string) (*driver.Table, error) {
	tab := t.c.schema.TableByName(name)
	if tab == nil {
		return nil, fmt.Errorf("sqlite: unknown table %q", name)
	}
	return tab, nil
}
func (t *transaction) readFile(table string, id uint64, field string) (io.ReadCloser, error) {
	var data []byte
	var size sql.NullInt64
	column := quote(field)
	err := t.tx.QueryRowContext(t.ctx, "SELECT "+column+",LENGTH(CAST("+column+" AS BLOB)) FROM "+quote(table)+" WHERE id=?", id).Scan(&data, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, lockError(err)
	}
	if !size.Valid || size.Int64 == 0 {
		return nil, nil
	}
	if data == nil {
		data = []byte{}
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (t *transaction) Get(table string, id uint64) (driver.DataObject, error) {
	tab, err := t.table(table)
	if err != nil {
		return nil, err
	}
	if id > math.MaxInt64 {
		return nil, driver.ErrNotFound
	}
	names := make([]string, 0, len(tab.Columns))
	for _, c := range tab.Columns {
		if c.Type == driver.TypeFile {
			names = append(names, "LENGTH(CAST("+quote(c.Name)+" AS BLOB))")
		} else {
			names = append(names, quote(c.Name))
		}
	}
	if len(names) == 0 {
		names = append(names, "id")
	}
	vals := make([]any, len(names))
	dest := make([]any, len(names))
	for i := range vals {
		dest[i] = &vals[i]
	}
	err = t.tx.QueryRowContext(t.ctx, "SELECT "+strings.Join(names, ",")+" FROM "+quote(table)+" WHERE id=?", id).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, driver.ErrNotFound
	}
	if err != nil {
		return nil, lockError(err)
	}
	out := driver.DataObject{}
	for i, c := range tab.Columns {
		v := vals[i]
		if v == nil {
			out[c.Name] = nil
			continue
		}
		switch c.Type {
		case driver.TypeFile:
			if v.(int64) == 0 {
				v = nil
			} else {
				field := c.Name
				v = driver.NewFile(func() (io.ReadCloser, error) {
					return t.readFile(table, id, field)
				})
			}
		case driver.TypeBinary:
			data, ok := v.([]byte)
			if !ok {
				return nil, fmt.Errorf("sqlite: invalid binary value in %s.%s: %T", table, c.Name, v)
			}
			if len(data) == 0 {
				v = nil
			}
		case driver.TypeID:
			v = uint64(v.(int64))
		case driver.TypeBoolean:
			v = v.(int64) != 0
		case driver.TypeDecimal, driver.TypeJSON:
			text, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("sqlite: invalid text value in %s.%s: %T", table, c.Name, v)
			}
			if c.Type == driver.TypeDecimal {
				v, err = decodeDecimal(text, c)
			} else {
				v, err = decodeJSON(text)
			}
		case driver.TypeDatetime:
			v = driver.Cast(driver.TypeDatetime, v)
		}
		if err != nil {
			return nil, err
		}
		out[c.Name] = v
	}
	return out, nil
}
func sortedKeys(data driver.DataObject) []string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func (t *transaction) Set(table string, id uint64, data driver.DataObject) (uint64, error) {
	tab, err := t.table(table)
	if err != nil {
		return 0, err
	}
	if id > math.MaxInt64 {
		return 0, fmt.Errorf("sqlite: invalid ID %d", id)
	}
	var columns, placeholders, updates []string
	var args, updateArgs []any
	if id != 0 {
		columns = append(columns, "id")
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	for _, name := range sortedKeys(data) {
		col := tab.ColumnByName(name)
		if col == nil || name == "id" {
			return 0, fmt.Errorf("sqlite: unknown or reserved column %q", name)
		}
		value, e := argument(data[name], col)
		if e != nil {
			return 0, e
		}
		columns = append(columns, quote(name))
		placeholders = append(placeholders, "?")
		args = append(args, value)
		updates = append(updates, quote(name)+"=?")
		updateArgs = append(updateArgs, value)
	}
	if id != 0 {
		if len(updates) == 0 {
			updates = append(updates, "id=id")
		}
		updateArgs = append(updateArgs, id)
		result, err := t.tx.ExecContext(t.ctx, "UPDATE "+quote(table)+" SET "+strings.Join(updates, ",")+" WHERE id=?", updateArgs...)
		if err != nil {
			return 0, lockError(err)
		}
		matched, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if matched != 0 {
			return id, nil
		}
	}
	query := "INSERT INTO " + quote(table)
	if len(columns) == 0 {
		query += " DEFAULT VALUES"
	} else {
		query += " (" + strings.Join(columns, ",") + ") VALUES (" + strings.Join(placeholders, ",") + ")"
	}
	result, err := t.tx.ExecContext(t.ctx, query, args...)
	if err != nil {
		return 0, lockError(err)
	}
	if id == 0 {
		generated, err := result.LastInsertId()
		if err != nil {
			return 0, err
		}
		return uint64(generated), nil
	}
	return id, nil
}

func (t *transaction) Increment(table string, id uint64, data driver.DataObject) error {
	tab, err := t.table(table)
	if err != nil {
		return err
	}
	if len(data) == 0 || id > math.MaxInt64 {
		return nil
	}
	var set []string
	var args []any
	for _, name := range sortedKeys(data) {
		col := tab.ColumnByName(name)
		if col == nil || (col.Type != driver.TypeDecimal && col.Type != driver.TypeInteger && col.Type != driver.TypeID) {
			return fmt.Errorf("sqlite: cannot increment %q", name)
		}
		q := quote(name)
		if col.Type == driver.TypeDecimal {
			n, e := decimalScaled(data[name], col)
			if e != nil {
				return e
			}
			set = append(set, q+"=mdd_decimal_add("+q+",?)")
			args = append(args, n.String())
		} else {
			set = append(set, q+"="+q+"+?")
			args = append(args, data[name])
		}
	}
	args = append(args, id)
	_, err = t.tx.ExecContext(t.ctx, "UPDATE "+quote(table)+" SET "+strings.Join(set, ",")+" WHERE id=?", args...)
	return lockError(err)
}
func (t *transaction) Delete(table string, id uint64) error {
	if _, err := t.table(table); err != nil {
		return err
	}
	if id > math.MaxInt64 {
		return nil
	}
	_, err := t.tx.ExecContext(t.ctx, "DELETE FROM "+quote(table)+" WHERE id=?", id)
	return lockError(err)
}
func (t *transaction) Fetch(q *driver.Query, fn func(uint64) error) (string, error) {
	if q == nil || fn == nil {
		return "", errors.New("sqlite: query and callback are required")
	}
	if q.Extra != nil {
		return "", errors.New("sqlite: extended queries are not supported")
	}
	plan, err := query.Resolve(t.c.schema, q)
	if err != nil {
		return "", fmt.Errorf("sqlite: %w", err)
	}
	idx := plan.Index
	from := plan.FromSQL(quote)
	idColumn, _ := plan.Column("", "id")
	idSQL := idColumn.SQL(quote)
	var where []string
	var args []any
	for _, f := range q.Filters {
		name := f.Column
		col, e := plan.Column(f.Path, name)
		if e != nil {
			return "", fmt.Errorf("sqlite: %w", e)
		}
		if col.Type == driver.TypeJSON {
			return "", errors.New("sqlite: JSON filters are not supported")
		}
		op := f.Op
		if op == "" {
			op = driver.OpEqual
		}
		switch op {
		case driver.OpEqual, driver.OpNotEqual, driver.OpGT, driver.OpGTE, driver.OpLT, driver.OpLTE, driver.OpMatch:
		default:
			return "", fmt.Errorf("sqlite: invalid operator %q", op)
		}
		if len(f.Values) == 0 {
			if op == driver.OpEqual {
				where = append(where, "1=0")
				continue
			}
			if op == driver.OpNotEqual {
				continue
			}
			return "", errors.New("sqlite: filter requires a value")
		}
		if len(f.Values) != 1 && op != driver.OpEqual && op != driver.OpNotEqual {
			return "", fmt.Errorf("sqlite: operator %q requires exactly one value", op)
		}
		var clauses []string
		for _, v := range f.Values {

			val, e := argument(v, col.Column)
			if e != nil {
				return "", e
			}
			if op == driver.OpMatch {
				clauses = append(clauses, "instr("+col.SQL(quote)+",?)>0")
			} else {
				clauses = append(clauses, col.SQL(quote)+op+"?")
			}
			args = append(args, val)
		}
		join := " OR "
		if op == driver.OpNotEqual {
			join = " AND "
		}
		where = append(where, "("+strings.Join(clauses, join)+")")
	}
	order := q.Order
	if len(order) == 0 && idx != nil {
		for _, name := range idx.Columns {
			order = append(order, driver.QueryOrder{Column: name})
		}
	}
	type sortKey struct {
		expr string
		desc bool
	}
	var keys []sortKey
	for _, item := range order {
		name := item.Column
		col, e := plan.Column(item.Path, name)
		if e != nil {
			return "", fmt.Errorf("sqlite: %w", e)
		}
		if col.Type == driver.TypeJSON {
			return "", errors.New("sqlite: cannot order JSON")
		}
		keys = append(keys, sortKey{col.SQL(quote), item.Desc})
	}
	if len(order) == 0 || order[len(order)-1].Path != "" || order[len(order)-1].Column != "id" {
		desc := len(order) > 0 && order[len(order)-1].Desc
		keys = append(keys, sortKey{idSQL, desc})
	}
	offset := uint64(0)
	if q.Offset != "" {
		offset, err = strconv.ParseUint(q.Offset, 10, 63)
		if err != nil {
			return "", fmt.Errorf("sqlite: invalid offset: %w", err)
		}
	}
	if offset > 0 {
		var alternatives []string
		for i, key := range keys {
			var parts []string
			for j := 0; j < i; j++ {
				parts = append(parts, keys[j].expr+" IS (SELECT "+keys[j].expr+" FROM "+from+" WHERE "+idSQL+"=?)")
				args = append(args, offset)
			}
			op := ">"
			if key.desc {
				op = "<"
			}
			cursor := "(SELECT " + key.expr + " FROM " + from + " WHERE " + idSQL + "=?)"
			nullCondition := key.expr + " IS NOT NULL AND " + cursor + " IS NULL"
			if key.desc {
				nullCondition = key.expr + " IS NULL AND " + cursor + " IS NOT NULL"
			}
			parts = append(parts, "("+key.expr+op+cursor+" OR ("+nullCondition+"))")
			args = append(args, offset, offset)
			alternatives = append(alternatives, "("+strings.Join(parts, " AND ")+")")
		}
		where = append(where, "("+strings.Join(alternatives, " OR ")+")")
	}
	query := "SELECT " + idSQL + " FROM " + from
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY "
	orderSQL := make([]string, len(keys))
	for i, key := range keys {
		orderSQL[i] = key.expr
		if key.desc {
			orderSQL[i] += " DESC"
		}
	}
	query += strings.Join(orderSQL, ",")
	if q.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, q.Limit)
	}
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return "", lockError(err)
	}
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", lockError(err)
	}
	last := offset
	for _, id := range ids {
		last = id
		if err = fn(id); err != nil {
			if driver.IsBreak(err) {
				return strconv.FormatUint(last, 10), nil
			}
			return "", err
		}
	}
	return strconv.FormatUint(last, 10), nil
}
