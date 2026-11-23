package mem

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/driver/internal/query"
)

func numeric(v any) (*big.Rat, error) {
	s, ok := v.(string)
	if !ok {
		s = fmt.Sprint(v)
	}
	n, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("mem: invalid numeric value %v", v)
	}
	return n, nil
}

func collectionNumber(v any) bool {
	switch v.(type) {
	case json.Number, float32, float64, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
}

func collectionMatches(value any, targets []any, requireAll bool) (bool, error) {
	if value == nil {
		return false, nil
	}
	if text, ok := value.(string); ok && text == "" {
		return false, nil
	}
	items := reflect.ValueOf(value)
	if items.Kind() != reflect.Slice && items.Kind() != reflect.Array {
		return false, fmt.Errorf("mem: JSON collection must be an array")
	}
	for _, target := range targets {
		found := false
		for i := 0; i < items.Len(); i++ {
			item := items.Index(i).Interface()
			if collectionNumber(item) && collectionNumber(target) {
				a, err := numeric(item)
				if err != nil {
					return false, err
				}
				b, err := numeric(target)
				if err != nil {
					return false, err
				}
				found = a.Cmp(b) == 0
			} else {
				switch v := item.(type) {
				case nil:
					found = target == nil
				case string:
					if t, ok := target.(string); ok {
						found = v == t
					}
				case bool:
					if t, ok := target.(bool); ok {
						found = v == t
					}
				}
			}
			if found {
				break
			}
		}
		if found != requireAll {
			return found, nil
		}
	}
	return requireAll, nil
}

func compare(a, b any, typ driver.DataType) (int, error) {
	if a == nil && b == nil {
		return 0, nil
	}
	if a == nil {
		return -1, nil
	}
	if b == nil {
		return 1, nil
	}
	switch typ {
	case driver.TypeID, driver.TypeInteger, driver.TypeDecimal:
		if typ == driver.TypeID {
			if x, ok := a.(uint64); ok {
				if y, ok := b.(uint64); ok {
					return cmp.Compare(x, y), nil
				}
			}
		} else if typ == driver.TypeInteger {
			if x, ok := a.(int64); ok {
				if y, ok := b.(int64); ok {
					return cmp.Compare(x, y), nil
				}
			}
		}
		x, err := numeric(a)
		if err != nil {
			return 0, err
		}
		y, err := numeric(b)
		if err != nil {
			return 0, err
		}
		return x.Cmp(y), nil
	case driver.TypeBoolean:
		x, ok := a.(bool)
		if !ok {
			return 0, fmt.Errorf("mem: invalid boolean %v", a)
		}
		y, ok := b.(bool)
		if !ok {
			return 0, fmt.Errorf("mem: invalid boolean %v", b)
		}
		if x == y {
			return 0, nil
		}
		if !x {
			return -1, nil
		}
		return 1, nil
	case driver.TypeDatetime:
		x, ok := a.(time.Time)
		if !ok {
			x = driver.Cast(a, driver.TypeDatetime, false).(time.Time)
		}
		y, ok := b.(time.Time)
		if !ok {
			y = driver.Cast(b, driver.TypeDatetime, false).(time.Time)
		}
		return x.Compare(y), nil
	case driver.TypeBinary, driver.TypeFile:
		if x, ok := a.([]byte); ok {
			if y, ok := b.([]byte); ok {
				return bytes.Compare(x, y), nil
			}
		}
		asString := func(v any) string {
			if b, ok := v.([]byte); ok {
				return string(b)
			}
			return fmt.Sprint(v)
		}
		return strings.Compare(asString(a), asString(b)), nil
	case driver.TypeString:
		if x, ok := a.(string); ok {
			if y, ok := b.(string); ok {
				return strings.Compare(x, y), nil
			}
		}
		return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)), nil
	default:
		return 0, errorsUnsupportedType(typ)
	}
}
func errorsUnsupportedType(typ driver.DataType) error {
	return fmt.Errorf("mem: cannot compare type %d", typ)
}

func (t *transaction) Increment(table string, id uint64, values driver.DataObject) error {
	tab, rows, err := t.table(table)
	if err != nil {
		return err
	}
	row, ok := rows[id]
	if !ok {
		return nil
	}
	updates := driver.DataObject{}
	for name, delta := range values {
		col := tab.ColumnByName(name)
		if col == nil || (col.Type != driver.TypeInteger && col.Type != driver.TypeDecimal && col.Type != driver.TypeID) {
			return fmt.Errorf("mem: cannot increment column %q", name)
		}
		left, err := numeric(row[name])
		if err != nil {
			return err
		}
		right, err := numeric(delta)
		if err != nil {
			return err
		}
		left.Add(left, right)
		switch col.Type {
		case driver.TypeInteger:
			if !left.IsInt() || !left.Num().IsInt64() {
				return fmt.Errorf("mem: integer overflow for %q", name)
			}
			updates[name] = left.Num().Int64()
		case driver.TypeID:
			if !left.IsInt() || !left.Num().IsUint64() {
				return fmt.Errorf("mem: ID overflow for %q", name)
			}
			updates[name] = left.Num().Uint64()
		case driver.TypeDecimal:
			updates[name] = left.FloatString(col.Scale)
		}
	}
	if indexes := t.conn.unique[tab]; len(indexes) != 0 {
		candidate := copyObject(row)
		maps.Copy(candidate, updates)
		if err := checkUnique(table, indexes, rows, id, candidate); err != nil {
			return err
		}
	}
	maps.Copy(row, updates)
	if len(updates) > 0 {
		t.dirty = true
	}
	return nil
}

func (t *transaction) Fetch(q *driver.Query, fn func(uint64) error) (string, error) {
	if q == nil || fn == nil {
		return "", fmt.Errorf("mem: query and callback are required")
	}
	if q.RawSQL != nil || len(q.SQLFilters) != 0 {
		return "", driver.ErrSQLUnsupported
	}
	if q.Extra != nil {
		return "", fmt.Errorf("mem: extended queries are not supported")
	}
	plan, err := query.Resolve(t.conn.schema, q)
	if err != nil {
		return "", fmt.Errorf("mem: %w", err)
	}
	index := plan.Index
	if err := t.check(); err != nil {
		return "", err
	}
	order := q.Order
	if len(order) == 0 && index != nil {
		order = make([]driver.QueryOrder, len(index.Columns))
		for i, name := range index.Columns {
			order[i] = driver.QueryOrder{Column: name}
		}
	}
	type columnKey struct {
		source string
		name   string
	}
	columns := make(map[columnKey]query.Column)
	resolveColumn := func(source, name string) (query.Column, error) {
		key := columnKey{source, name}
		if col, ok := columns[key]; ok {
			return col, nil
		}
		col, err := plan.Column(source, name)
		if err != nil {
			return query.Column{}, fmt.Errorf("mem: %w", err)
		}
		columns[key] = col
		return col, nil
	}
	orderColumns := make([]query.Column, len(order))
	for i, item := range order {
		col, e := resolveColumn(item.Source, item.Column)
		if e != nil {
			return "", e
		}
		if col.Type == driver.TypeJSON {
			return "", errorsUnsupportedType(col.Type)
		}
		orderColumns[i] = col
	}
	type resolvedFilter struct {
		column query.Column
		op     string
		values []any
	}
	filters := make([]resolvedFilter, len(q.Filters))
	for i, f := range q.Filters {
		op := f.Op
		if op == "" {
			op = driver.OpEqual
		}
		col, e := resolveColumn(f.Source, f.Column)
		if e != nil {
			return "", e
		}
		filters[i] = resolvedFilter{column: col, op: op, values: f.Values}
		typ := col.Type
		if op == driver.OpContainsAll || op == driver.OpContainsAny {
			if typ != driver.TypeJSON {
				return "", fmt.Errorf("mem: %s requires a JSON collection", op)
			}
			if len(f.Values) == 0 {
				return "", fmt.Errorf("mem: operator %s requires at least one value", op)
			}
			continue
		}
		if typ == driver.TypeJSON {
			return "", errorsUnsupportedType(typ)
		}
		switch op {
		case driver.OpEqual, driver.OpNotEqual, driver.OpGT, driver.OpGTE, driver.OpLT, driver.OpLTE, driver.OpMatch:
		default:
			return "", fmt.Errorf("mem: unsupported operator %q", f.Op)
		}
		if len(f.Values) == 0 && op != driver.OpEqual && op != driver.OpNotEqual {
			return "", fmt.Errorf("mem: empty filter values")
		}
		if len(f.Values) > 1 && op != driver.OpEqual && op != driver.OpNotEqual {
			return "", fmt.Errorf("mem: multiple values require equality or inequality")
		}
	}
	for i := range filters {
		typ := filters[i].column.Type
		if typ == driver.TypeBinary || typ == driver.TypeFile {
			filters[i].values = make([]any, len(q.Filters[i].Values))
			for j, value := range q.Filters[i].Values {
				data, err := driver.FileBytes(value)
				if err != nil {
					return "", err
				}
				filters[i].values[j] = data
			}
		}
	}
	sourceRows := make([]map[uint64]driver.DataObject, len(plan.Sources))
	for i, source := range plan.Sources {
		sourceRows[i] = t.state.Tables[source.Table.Name]
	}
	rows := sourceRows[0]
	var sourceRow func(uint64, int) (uint64, driver.DataObject, error)
	sourceRow = func(id uint64, source int) (uint64, driver.DataObject, error) {
		if source == 0 {
			return id, rows[id], nil
		}
		join := plan.Sources[source]
		parentID, parent, err := sourceRow(id, join.Parent)
		if err != nil || parent == nil {
			return 0, nil, err
		}
		reference := any(parentID)
		if join.Key != "id" {
			reference = parent[join.Key]
		}
		if reference == nil {
			return 0, nil, nil
		}
		targetID, ok := reference.(uint64)
		if !ok {
			number, err := numeric(reference)
			if err != nil || !number.IsInt() || !number.Num().IsUint64() {
				return 0, nil, fmt.Errorf("mem: invalid join ID %v in %s", reference, join.Key)
			}
			targetID = number.Num().Uint64()
		}
		return targetID, sourceRows[source][targetID], nil
	}
	value := func(id uint64, col query.Column) (any, error) {
		joinedID, row, err := sourceRow(id, col.Source)
		if err != nil || row == nil {
			return nil, err
		}
		if col.Name == "id" {
			return joinedID, nil
		}
		return row[col.Name], nil
	}
	ids := make([]uint64, 0, len(rows))
	for id := range rows {
		if err = t.check(); err != nil {
			return "", err
		}
		matched := true
		for _, f := range filters {
			if len(f.values) == 0 {
				if f.op == driver.OpEqual {
					matched = false
					break
				}
				continue
			}
			typ, op := f.column.Type, f.op
			v, err := value(id, f.column)
			if err != nil {
				return "", err
			}
			if op == driver.OpContainsAll || op == driver.OpContainsAny {
				ok, err := collectionMatches(v, f.values, op == driver.OpContainsAll)
				if err != nil {
					return "", err
				}
				if !ok {
					matched = false
					break
				}
				continue
			}
			anyMatch := op == driver.OpNotEqual
			for _, target := range f.values {
				if target == nil {
					target = driver.DefaultValue(typ)
				}
				if v == nil || target == nil {
					switch op {
					case "", driver.OpEqual:
						anyMatch = anyMatch || (v == nil && target == nil)
					case driver.OpNotEqual:
						anyMatch = anyMatch && target == nil && v != nil
					}
					continue
				}
				if op == driver.OpMatch {
					if strings.Contains(fmt.Sprint(v), fmt.Sprint(target)) {
						anyMatch = true
					}
					continue
				}
				cmp, e := compare(v, target, typ)
				if e != nil {
					return "", e
				}
				switch op {
				case "", driver.OpEqual:
					anyMatch = anyMatch || cmp == 0
				case driver.OpNotEqual:
					anyMatch = anyMatch && cmp != 0
				case driver.OpGT:
					anyMatch = anyMatch || cmp > 0
				case driver.OpGTE:
					anyMatch = anyMatch || cmp >= 0
				case driver.OpLT:
					anyMatch = anyMatch || cmp < 0
				case driver.OpLTE:
					anyMatch = anyMatch || cmp <= 0
				}
			}
			if !anyMatch {
				matched = false
				break
			}
		}
		if matched {
			ids = append(ids, id)
		}
	}
	var sortErr error
	sort.Slice(ids, func(i, j int) bool {
		for index, column := range orderColumns {
			left, e := value(ids[i], column)
			if e != nil {
				sortErr = e
				return ids[i] < ids[j]
			}
			right, e := value(ids[j], column)
			if e != nil {
				sortErr = e
				return ids[i] < ids[j]
			}
			cmp, e := compare(left, right, column.Type)
			if e != nil {
				sortErr = e
				return ids[i] < ids[j]
			}
			if cmp != 0 {
				if order[index].Desc {
					return cmp > 0
				}
				return cmp < 0
			}
		}
		if len(order) > 0 && order[len(order)-1].Desc {
			return ids[i] > ids[j]
		}
		return ids[i] < ids[j]
	})
	if sortErr != nil {
		return "", sortErr
	}
	offset := uint64(0)
	if q.Offset != "" {
		offset, err = strconv.ParseUint(q.Offset, 10, 64)
		if err != nil {
			return "", fmt.Errorf("mem: invalid offset: %w", err)
		}
	}
	start := 0
	if offset != 0 {
		found := false
		for i, id := range ids {
			if id == offset {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("mem: offset %d is not in the result", offset)
		}
	}
	last := offset
	var count int64
	for _, id := range ids[start:] {
		if err = t.check(); err != nil {
			return "", err
		}
		last = id
		if err = fn(id); err != nil {
			if driver.IsBreak(err) {
				return strconv.FormatUint(last, 10), nil
			}
			return "", err
		}
		count++
		if q.Limit > 0 && count >= q.Limit {
			break
		}
	}
	return strconv.FormatUint(last, 10), nil
}
