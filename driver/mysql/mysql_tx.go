package mysql

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/goldic/js"
	"github.com/metadata-driven/mdd/driver"
	queryplan "github.com/metadata-driven/mdd/driver/internal/query"
)

type transaction struct {
	conn *Connection
	ctx  context.Context
	db   *sql.DB
	dbTx *sql.Tx
}

type dbColumnInfo struct {
	Name     string
	Type     string
	Nullable bool
}

type dbIndexInfo struct {
	Name    string
	Type    driver.IndexType
	Columns []string
	Managed bool
}

func (c *Connection) openDBConnection() (db *sql.DB, err error) {
	connStr := c.Params().String()
	connStr = strings.TrimPrefix(connStr, "mysql://")

	config, err := mysqlDriver.ParseDSN(connStr)
	if err != nil {
		return nil, err
	}
	config.ParseTime = true
	config.Loc = time.UTC
	config.ClientFoundRows = true
	return sql.Open("mysql", config.FormatDSN())
}

func newTransaction(c *Connection, ctx context.Context) (_ *transaction, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dbTx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	return &transaction{
		conn: c,
		ctx:  ctx,
		db:   c.db,
		dbTx: dbTx,
	}, nil
}

func decodeJSON(data []byte) (any, error) {
	if len(data) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func (t *transaction) Rollback() {
	t.dbTx.Rollback()
}

func (t *transaction) Commit() (err error) {
	return t.dbTx.Commit()
}

func (t *transaction) getFile(table string, id uint64, field string) driver.File {
	return driver.NewFile(func() (io.ReadCloser, error) {
		return t.readFile(table, id, field)
	})
}

func (t *transaction) readFile(table string, id uint64, field string) (io.ReadCloser, error) {
	var data []byte
	var size sql.NullInt64
	column := escName(field)
	err := t.dbTx.QueryRowContext(t.ctx, t.traceSQL("SELECT "+column+",LENGTH("+column+") FROM "+escName(table)+" WHERE id=?"), id).Scan(&data, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil || !size.Valid || size.Int64 == 0 {
		return nil, err
	}
	if data == nil {
		data = []byte{}
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (t *transaction) Get(entity string, id uint64) (obj driver.DataObject, err error) {
	if t.conn.scheme.TableByName(entity) == nil {
		return nil, fmt.Errorf("mysql: unknown table %q", entity)
	}
	objects, err := t.MultiGet(entity, []uint64{id})
	if err != nil {
		return nil, err
	}
	if len(objects) > 0 {
		return objects[0], nil
	}
	return nil, driver.ErrNotFound
}

func (t *transaction) MultiGet(entity string, ids []uint64) (objs []driver.DataObject, err error) {
	if len(ids) == 0 {
		return []driver.DataObject{}, nil
	}

	// "SELECT col,... FROM %s WHERE id IN(<id,...>)", escName(entity))
	// todo: cache q,cc by entity
	cc := t.conn.scheme.TableByName(entity).Columns
	q := bytes.NewBufferString("SELECT id")
	pp := make([]any, len(cc))
	for i, col := range cc {
		q.WriteByte(',')
		if col.Type == driver.TypeFile {
			q.WriteString("LENGTH(" + escName(col.Name) + ")")
		} else {
			q.WriteString(escName(col.Name))
		}
		switch col.Type {
		case driver.TypeID:
			pp[i] = new(uint64)
		case driver.TypeBoolean:
			pp[i] = new(bool)
		case driver.TypeInteger:
			pp[i] = new(int64)
		case driver.TypeDatetime:
			pp[i] = new(time.Time)
		case driver.TypeFile:
			pp[i] = new(int64)
		case driver.TypeBinary, driver.TypeJSON:
			pp[i] = new([]byte)
		default:
			sNil := new(string)
			pp[i] = &sNil
		}
	}
	q.WriteString(" FROM ")
	q.WriteString(escName(entity))
	q.WriteString(" WHERE id IN(")
	for i, id := range ids {
		if i > 0 {
			q.WriteByte(',')
		}
		q.WriteString(strconv.FormatUint(id, 10))
	}
	q.WriteString(")")
	//
	rows, err := t.dbTx.QueryContext(t.ctx, t.traceSQL(q.String()))
	if err != nil {
		return
	}
	defer rows.Close()

	objs = make([]driver.DataObject, 0, len(ids))
	var id uint64
	scanArgs := append([]any{&id}, pp...)
	for rows.Next() {
		if err = rows.Scan(scanArgs...); err != nil {
			return
		}
		obj := make(driver.DataObject, len(cc))
		for i, col := range cc {
			switch col.Type {
			case driver.TypeID:
				obj[col.Name] = *(pp[i].(*uint64))
			case driver.TypeBoolean:
				obj[col.Name] = *(pp[i].(*bool))
			case driver.TypeInteger:
				obj[col.Name] = *(pp[i].(*int64))
			case driver.TypeFile:
				if size := *(pp[i].(*int64)); size > 0 {
					obj[col.Name] = t.getFile(entity, id, col.Name)
				} else {
					obj[col.Name] = nil
				}
			case driver.TypeBinary:
				data := *(pp[i].(*[]byte))
				if len(data) > 0 {
					obj[col.Name] = bytes.Clone(data)
				} else {
					obj[col.Name] = nil
				}
			case driver.TypeJSON:
				if data := *(pp[i].(*[]byte)); data != nil {
					value, decodeErr := decodeJSON(data)
					if decodeErr != nil {
						return nil, fmt.Errorf("mysql> invalid JSON in %s.%s: %w", entity, col.Name, decodeErr)
					}
					obj[col.Name] = value
				} else {
					obj[col.Name] = nil
				}
			case driver.TypeDatetime:
				obj[col.Name] = *(pp[i].(*time.Time))
			default:
				if p := *(pp[i].(**string)); p == nil {
					obj[col.Name] = nil
				} else {
					obj[col.Name] = *p
				}
			}
		}
		objs = append(objs, obj)
	}
	err = rows.Err()
	return
}

func (t *transaction) Set(entity string, id uint64, data driver.DataObject) (uint64, error) {
	tab := t.conn.scheme.TableByName(entity)
	if tab == nil {
		return 0, fmt.Errorf("mysql: unknown table %q", entity)
	}
	var columns, placeholders []string
	var args []any
	var updates []string
	var updateArgs []any
	if id != 0 {
		columns = append(columns, "id")
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	for name, value := range data {
		col := tab.ColumnByName(name)
		if col == nil || name == "id" {
			return 0, fmt.Errorf("mysql: unknown or reserved column %q", name)
		}
		var err error
		if col.Type == driver.TypeJSON {
			value, err = driver.JSONText(value)
		} else {
			value, err = driver.NormalizeColumnValue(col, value)
		}
		if err != nil {
			return 0, fmt.Errorf("mysql: %s.%s: %w", entity, name, err)
		}
		columns = append(columns, escName(name))
		placeholders = append(placeholders, "?")
		args = append(args, value)
		if id != 0 {
			updates = append(updates, escName(name)+"=?")
			updateArgs = append(updateArgs, value)
		}
	}
	if id != 0 {
		if len(updates) == 0 {
			updates = append(updates, "id=id")
		}
		updateArgs = append(updateArgs, id)
		result, err := t.dbTx.ExecContext(t.ctx, t.traceSQL("UPDATE "+escName(entity)+" SET "+strings.Join(updates, ",")+" WHERE id=?"), updateArgs...)
		if err != nil {
			return 0, err
		}
		matched, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if matched != 0 {
			return id, nil
		}
	}
	query := "INSERT INTO " + escName(entity) + " (" + strings.Join(columns, ",") + ") VALUES (" + strings.Join(placeholders, ",") + ")"
	result, err := t.dbTx.ExecContext(t.ctx, t.traceSQL(query), args...)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		actual, err := result.LastInsertId()
		if err != nil {
			return 0, err
		}
		return uint64(actual), nil
	}
	return id, nil
}

func (t *transaction) Increment(entity string, id uint64, data driver.DataObject) (err error) {
	// todo: prepare query; sort values for prepared-query
	tab := t.conn.scheme.TableByName(entity)
	if tab == nil {
		return fmt.Errorf("mysql: unknown table %q", entity)
	}
	if len(data) == 0 {
		return nil
	}

	expr := bytes.NewBuffer(nil)
	for name, v := range data {
		col := tab.ColumnByName(name)
		if col == nil || (col.Type != driver.TypeID && col.Type != driver.TypeInteger && col.Type != driver.TypeDecimal) {
			return fmt.Errorf("mysql: cannot increment column %q", name)
		}
		if expr.Len() > 0 {
			expr.WriteString(",")
		}
		name = escName(name)
		expr.WriteString(name)
		expr.WriteString("=")
		expr.WriteString(name)
		expr.WriteString("+")
		expr.WriteString(escVal(v))
	}
	_, err = t.dbTx.ExecContext(t.ctx, t.traceSQL(fmt.Sprintf(
		"UPDATE %s SET %s WHERE id=%d",
		escName(entity),
		expr.String(),
		id,
	)))
	return
}

func (t *transaction) Delete(entity string, id uint64) (err error) {
	if t.conn.scheme.TableByName(entity) == nil {
		return fmt.Errorf("mysql: unknown table %q", entity)
	}
	_, err = t.dbTx.ExecContext(t.ctx, t.traceSQL(fmt.Sprintf(
		"DELETE FROM %s WHERE id=%d",
		escName(entity),
		id,
	)))
	return
}

func (t *transaction) Fetch(query *driver.Query, fn func(id uint64) error) (nextOffset string, err error) {
	if query == nil || fn == nil {
		return "", fmt.Errorf("mysql: query and callback are required")
	}
	if query.Extra != nil {
		return "", fmt.Errorf("mysql: extended queries are not supported")
	}
	plan, err := queryplan.Resolve(t.conn.scheme, query)
	if err != nil {
		return "", fmt.Errorf("mysql: %w", err)
	}
	idx := plan.Index
	from := plan.FromSQL(escName)
	idColumn, _ := plan.Column("", "id")
	idSQL := idColumn.SQL(escName)
	var where []string
	var args []any
	for _, f := range query.Filters {
		name := f.Column
		col, err := plan.Column(f.Path, name)
		if err != nil {
			return "", fmt.Errorf("mysql: %w", err)
		}
		op := f.Op
		if op == "" {
			op = driver.OpEqual
		}
		switch op {
		case driver.OpEqual, driver.OpNotEqual, driver.OpGT, driver.OpGTE, driver.OpLT, driver.OpLTE, driver.OpMatch:
		default:
			return "", fmt.Errorf("mysql: invalid operator %q", op)
		}
		if len(f.Values) == 0 {
			if op == driver.OpEqual {
				where = append(where, "1=0")
				continue
			}
			if op == driver.OpNotEqual {
				continue
			}
			return "", fmt.Errorf("mysql: filter requires a value")
		}
		if len(f.Values) != 1 && op != driver.OpEqual && op != driver.OpNotEqual {
			return "", fmt.Errorf("mysql: operator %q requires exactly one value", op)
		}
		var clauses []string
		for _, value := range f.Values {
			if col.Type == driver.TypeFile {
				value, err = driver.FileBytes(value)
				if err != nil {
					return "", err
				}
			}
			if value == nil {
				value = driver.DefaultValue(col.Type)
			}
			if op == driver.OpMatch {
				clauses = append(clauses, "LOCATE(?, "+col.SQL(escName)+") > 0")
			} else {
				clauses = append(clauses, col.SQL(escName)+op+"?")
			}
			args = append(args, value)
		}
		join := " OR "
		if op == driver.OpNotEqual {
			join = " AND "
		}
		where = append(where, "("+strings.Join(clauses, join)+")")
	}
	order := append([]driver.QueryOrder(nil), query.Order...)
	if len(order) == 0 && idx != nil {
		for _, name := range idx.Columns {
			order = append(order, driver.QueryOrder{Column: name})
		}
	}
	if len(order) == 0 || order[len(order)-1].Path != "" || order[len(order)-1].Column != "id" {
		desc := len(order) > 0 && order[len(order)-1].Desc
		order = append(order, driver.QueryOrder{Column: "id", Desc: desc})
	}
	orderSQL := make([]string, len(order))
	for i, item := range order {
		col, err := plan.Column(item.Path, item.Column)
		if err != nil {
			return "", fmt.Errorf("mysql: %w", err)
		}
		orderSQL[i] = col.SQL(escName)
	}
	offsetID := uint64(0)
	if query.Offset != "" {
		offsetID, err = strconv.ParseUint(query.Offset, 10, 64)
		if err != nil {
			return "", fmt.Errorf("mysql: invalid offset: %w", err)
		}
	}
	if offsetID > 0 {
		var alternatives []string
		for i, item := range order {
			var parts []string
			for j := range i {
				name := orderSQL[j]
				parts = append(parts, name+"<=>(SELECT "+name+" FROM "+from+" WHERE "+idSQL+"=?)")
				args = append(args, offsetID)
			}
			op := ">"
			if item.Desc {
				op = "<"
			}
			name := orderSQL[i]
			cursor := "(SELECT " + name + " FROM " + from + " WHERE " + idSQL + "=?)"
			nullCondition := name + " IS NOT NULL AND " + cursor + " IS NULL"
			if item.Desc {
				nullCondition = name + " IS NULL AND " + cursor + " IS NOT NULL"
			}
			parts = append(parts, "("+name+op+cursor+" OR ("+nullCondition+"))")
			args = append(args, offsetID, offsetID)
			alternatives = append(alternatives, "("+strings.Join(parts, " AND ")+")")
		}
		where = append(where, "("+strings.Join(alternatives, " OR ")+")")
	}
	b := bytes.NewBufferString("SELECT " + idSQL + " FROM ")
	b.WriteString(from)
	if len(where) != 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(where, " AND "))
	}
	b.WriteString(" ORDER BY ")
	for i, item := range order {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(orderSQL[i])
		if item.Desc {
			b.WriteString(" DESC")
		}
	}
	if query.Limit > 0 {
		b.WriteString(" LIMIT ?")
		args = append(args, query.Limit)
	}

	//------ exec
	// todo: fetch ids by batches(~1-100k); don`t request all ids
	rows, err := t.dbTx.QueryContext(t.ctx, t.traceSQL(b.String()), args...)
	if err != nil {
		return
	}
	ids := func() (res []uint64) {
		defer rows.Close()
		var id uint64
		for rows.Next() {
			if err = rows.Scan(&id); err != nil {
				return
			}
			res = append(res, id)
		}
		err = rows.Err()
		return
	}()
	if err != nil {
		return
	}
	//------ fetch results
	for _, id := range ids {
		offsetID = id
		if err = fn(id); err != nil {
			if driver.IsBreak(err) {
				err = nil
				break
			}
			return
		}
	}
	// set new offset
	nextOffset = js.ToStr(offsetID)
	return
}

func (t *transaction) traceSQL(q string) string {
	if SQLTracer != nil {
		SQLTracer.Printf("mysql> %s;", q)
	}
	return q
}

var (
	replEscName = strings.NewReplacer(".", "__", "`", "-")
	replEscVal  = strings.NewReplacer("\\", "\\\\", "'", `\'`, "\\0", "\\\\0", "\n", "\\n", "\r", "\\r", `"`, `\"`, "\x1a", "\\Z")
)

func escName(s string) string {
	return "`" + replEscName.Replace(s) + "`"
}

func escVal(v any) string {
	switch v := v.(type) {
	case bool:
		if v {
			return "1"
		}
		return "0"
	case int, int32, int64, uint, uint32, uint64, float32, float64:
		return fmt.Sprint(v)
	case string:
		return "'" + replEscVal.Replace(v) + "'"
	case []byte:
		return "'" + replEscVal.Replace(string(v)) + "'"
	case time.Time:
		return "'" + v.Format("2006-01-02 15:04:05") + "'"
	}
	panic(fmt.Errorf("mysql> unknown value-type: %t", v))
}

func dbColumnDefinition(col *driver.Column) string {
	switch col.Type {
	case driver.TypeID,
		driver.TypeBoolean,
		driver.TypeInteger,
		driver.TypeDecimal:
		return fmt.Sprintf("%s %s NOT NULL DEFAULT 0", escName(col.Name), dbColumnType(col))
	case driver.TypeString:
		if typ := dbColumnType(col); typ == "longtext" {
			return fmt.Sprintf("%s %s NOT NULL DEFAULT ('')", escName(col.Name), typ)
		} else {
			return fmt.Sprintf("%s %s NOT NULL DEFAULT ''", escName(col.Name), typ)
		}
	case driver.TypeDatetime:
		return fmt.Sprintf("%s %s NOT NULL DEFAULT '1970-01-01 00:00:00.000000'", escName(col.Name), dbColumnType(col))
	case driver.TypeBinary, driver.TypeFile, driver.TypeJSON:
		return fmt.Sprintf("%s %s NOT NULL DEFAULT ('')", escName(col.Name), dbColumnType(col))
	}
	panic(fmt.Errorf("mysql> unknown column type: %d", col.Type))
}

func dbColumnType(col *driver.Column) string {
	switch col.Type {
	case driver.TypeID:
		return "bigint unsigned"
	case driver.TypeBoolean:
		return "tinyint"
	case driver.TypeInteger:
		return "bigint"
	case driver.TypeDecimal:
		return fmt.Sprintf("decimal(%d,%d)", col.Precision, col.Scale)
	case driver.TypeString:
		if col.Length > 0 && col.Length < 1024 {
			return fmt.Sprintf("varchar(%d)", col.Length)
		}
		return "longtext"
	case driver.TypeDatetime:
		return "datetime(6)"
	case driver.TypeBinary:
		if col.Length < 0 {
			panic(fmt.Errorf("mysql: invalid binary length %d", col.Length))
		}
		if col.Length > 0 && col.Length <= 3072 {
			return fmt.Sprintf("varbinary(%d)", col.Length)
		}
		return "longblob"
	case driver.TypeFile:
		return "longblob"
	case driver.TypeJSON:
		return "longtext"
	}
	panic(fmt.Errorf("mysql> unknown column type: %d", col.Type))
	// by default ID
	//return escName(col.Name) + " bigint(20) unsigned NOT NULL DEFAULT 0"
}

func (t *transaction) normalizeNullColumn(table string, col *driver.Column, nullable bool) {
	var fallback string
	switch col.Type {
	case driver.TypeID, driver.TypeBoolean, driver.TypeInteger, driver.TypeDecimal:
		fallback = "0"
	case driver.TypeDatetime:
		fallback = "'1970-01-01 00:00:00'"
	case driver.TypeBinary, driver.TypeFile, driver.TypeJSON, driver.TypeString:
		fallback = "''"
	default:
		panic(fmt.Errorf("mysql: unsupported column type %d", col.Type))
	}
	column := escName(col.Name)
	condition := column + " IS NULL"
	if col.Type == driver.TypeJSON {
		condition += " OR " + column + " = 'null'"
	} else if !nullable {
		return
	}
	t.exec("UPDATE " + escName(table) + " SET " + column + " = " + fallback + " WHERE " + condition)
}

func (t *transaction) exec(query string) {
	_, err := t.db.Exec(t.traceSQL(query))
	noErr(err)
	//rowsAffected, err = res.RowsAffected()
	//t.assertNoErr(err)
}

func (t *transaction) queryAll(query string, args ...any) (res []js.Array) {
	rows := noErrVal(t.db.Query(t.traceSQL(query), args...))
	defer rows.Close()

	cols := noErrVal(rows.Columns())
	n := len(cols)
	ss := make([]any, n)
	ii := make([]any, n)
	for i := range ii {
		ii[i] = &ss[i]
	}
	for rows.Next() {
		noErr(rows.Scan(ii...))
		cc := make([]any, n)
		copy(cc, ss)
		res = append(res, cc)
	}
	noErr(rows.Err())
	return
}

func (t *transaction) CreateTable(tableName string) {
	t.exec(fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s(`id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY)",
		escName(tableName),
	))
}

func (t *transaction) DropTable(tableName string) {
	t.exec(fmt.Sprintf(
		"DROP TABLE IF EXISTS %s",
		escName(tableName),
	))
}

func (t *transaction) RenameTable(tableOldName, tableNewName string) {
	tableOldName = strings.ToLower(tableOldName)
	tableNewName = strings.ToLower(tableNewName)
	if tableOldName == tableNewName {
		return
	}
	t.exec(fmt.Sprintf(
		"RENAME TABLE %s TO %s",
		escName(tableOldName),
		escName(tableNewName),
	))
}

func (t *transaction) AddTableColumn(tableName string, col *driver.Column) {
	t.exec(fmt.Sprintf(
		"ALTER TABLE %s ADD COLUMN %s",
		escName(tableName),
		dbColumnDefinition(col),
	))
	//.($primary?" AUTO_INCREMENT PRIMARY KEY":"")
	//.($after?" AFTER `$after` ":"")
}

func (t *transaction) DropTableColumn(tableName, columnName string) {
	t.exec(fmt.Sprintf(
		"ALTER TABLE %s DROP COLUMN %s",
		escName(tableName),
		escName(columnName),
	))
}

func (t *transaction) AlterTableColumn(tableName string, col *driver.Column, oldname string) {
	if oldname == "" {
		oldname = col.Name
	}
	t.exec(fmt.Sprintf(
		"ALTER TABLE %s CHANGE COLUMN %s %s",
		escName(tableName),
		escName(oldname),
		dbColumnDefinition(col),
	))
	// todo: drop and create column on Error 1366: Incorrect integer value: '' for column 'brand' at row 1
}

func (t *transaction) AddTableIndex(tableName string, idx *driver.Index) {
	var idxType string
	switch idx.Type {
	case driver.IndexTypeIndex, "":
		idxType = ""
	case driver.IndexTypeUnique:
		idxType = "UNIQUE"
		for _, name := range idx.Columns {
			col := t.conn.scheme.TableByName(tableName).ColumnByName(name)
			if col != nil && ((col.Type == driver.TypeBinary && dbColumnType(col) == "longblob") || col.Type == driver.TypeFile || col.Type == driver.TypeJSON) {
				panic(fmt.Errorf("mysql: unique index %s cannot index %s.%s in full", idx.Name, tableName, name))
			}
		}
	case driver.IndexTypeFulltext:
		idxType = "FULLTEXT"
	default:
		panic(fmt.Errorf("mysql> unknown index type `%s`", idx.Type))
	}
	t.exec(fmt.Sprintf(
		"ALTER TABLE %s ADD %s INDEX %s(`%s`) COMMENT 'mdd managed'",
		escName(tableName),
		idxType,
		escName(idx.Name),
		strings.Join(idx.Columns, "`,`"),
	))
}

func (t *transaction) DropTableIndex(tableName, idxName string) {
	t.exec(fmt.Sprintf(
		"ALTER TABLE %s DROP INDEX %s",
		escName(tableName),
		escName(idxName),
	))
}

// ------- get DDL-structure
func (t *transaction) Tables() (tables []string) {
	for _, vv := range t.queryAll("SHOW TABLES") {
		tables = append(tables, vv.Eq(0).String())
	}
	return
}

func (t *transaction) TableColumns(tableName string) (cols []*dbColumnInfo) {
	for _, vv := range t.queryAll(fmt.Sprintf("SHOW COLUMNS FROM %s", escName(tableName))) {
		cols = append(cols, &dbColumnInfo{
			Name:     vv.Eq(0).String(),
			Type:     strings.ToLower(vv.Eq(1).String()),
			Nullable: strings.EqualFold(vv.Eq(2).String(), "YES"),
		})
	}
	return
}

func (t *transaction) TableIndexes(tableName string) (idxs []*dbIndexInfo) {
	//mysql> show indexes from _test;
	//+-------+------------+----------+--------------+-------------+-----------+-------------+----------+--------+------+------------+---------+---------------+---------+------------+
	//| Table | Non_unique | Key_name | Seq_in_index | Column_name | Collation | Cardinality | Sub_part | Packed | Null | Index_type | Comment | Index_comment | Visible | Expression |
	//+-------+------------+----------+--------------+-------------+-----------+-------------+----------+--------+------+------------+---------+---------------+---------+------------+
	//| _test |          0 | PRIMARY  |            1 | id          | A         |           0 |     NULL |   NULL |      | BTREE      |         |               | YES     | NULL       |
	//| _test |          0 | idx_uid  |            1 | uid         | A         |           0 |     NULL |   NULL |      | BTREE      |         |               | YES     | NULL       |
	//| _test |          1 | idx_ts   |            1 | ts          | A         |           0 |     NULL |   NULL |      | BTREE      |         |               | YES     | NULL       |
	//| _test |          1 | idx_title|            1 | title       | A         |           0 |     NULL |   NULL |      | BTREE      |         |               | YES     | NULL       |
	//| _test |          1 | idx_title|            2 | ts          | A         |           0 |     NULL |   NULL |      | BTREE      |         |               | YES     | NULL       |
	//| _test |          1 | idx_name |            1 | name        | NULL      |           0 |     NULL |   NULL |      | FULLTEXT   |         |               | YES     | NULL       |
	//+-------+------------+----------+--------------+-------------+-----------+-------------+----------+--------+------+------------+---------+---------------+---------+------------+
	//
	for _, vv := range t.queryAll(fmt.Sprintf("SHOW INDEXES FROM %s", escName(tableName))) {
		if vv.Eq(3).Int() > 1 { // Seq_in_index
			idx := idxs[len(idxs)-1]
			idx.Columns = append(idx.Columns, vv.Eq(4).String())
			continue
		}
		idx := &dbIndexInfo{
			Name:    vv.Eq(2).String(),
			Type:    driver.IndexType(strings.ToLower(vv.Eq(10).String())),
			Managed: vv.Eq(12).String() == "mdd managed",
			Columns: []string{vv.Eq(4).String()},
		}
		if vv.Eq(1).Int() == 0 && idx.Name != "PRIMARY" {
			idx.Type = driver.IndexTypeUnique
		} else if idx.Type == "btree" {
			idx.Type = driver.IndexTypeIndex // std index
		}
		idxs = append(idxs, idx)
	}
	return
}
