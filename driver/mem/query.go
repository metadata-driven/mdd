package mem

import (
	"bytes"
	"cmp"
	"fmt"
	"math/big"
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
			x = driver.Cast(driver.TypeDatetime, a).(time.Time)
		}
		y, ok := b.(time.Time)
		if !ok {
			y = driver.Cast(driver.TypeDatetime, b).(time.Time)
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
	rows, err := t.table(table)
	if err != nil {
		return err
	}
	row, ok := rows[id]
	if !ok {
		return nil
	}
	updates := driver.DataObject{}
	for name, delta := range values {
		col := t.conn.schema.TableByName(table).ColumnByName(name)
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
	if tab := t.conn.schema.TableByName(table); hasUnique(tab) {
		candidate := copyObject(row)
		for name, value := range updates {
			candidate[name] = value
		}
		if err := checkUnique(tab, rows, id, candidate); err != nil {
			return err
		}
	}
	for name, value := range updates {
		row[name] = value
	}
	if len(updates) > 0 {
		t.dirty = true
	}
	return nil
}

func (t *transaction) Fetch(q *driver.Query, fn func(uint64) error) (string, error) {
	if q == nil || fn == nil {
		return "", fmt.Errorf("mem: query and callback are required")
	}
	if q.Extra != nil {
		return "", fmt.Errorf("mem: extended queries are not supported")
	}
	plan, err := query.Resolve(t.conn.schema, q)
	if err != nil {
		return "", fmt.Errorf("mem: %w", err)
	}
	tab, index := plan.Sources[0].Table, plan.Index
	rows, err := t.table(tab.Name)
	if err != nil {
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
	colType := func(source, name string) (driver.DataType, error) {
		key := columnKey{source, name}
		if col, ok := columns[key]; ok {
			return col.Type, nil
		}
		col, err := plan.Column(source, name)
		if err != nil {
			return 0, fmt.Errorf("mem: %w", err)
		}
		columns[key] = col
		return col.Type, nil
	}
	for _, item := range order {
		typ, e := colType(item.Source, item.Column)
		if e != nil {
			return "", e
		}
		if typ == driver.TypeJSON {
			return "", errorsUnsupportedType(typ)
		}
	}
	for _, f := range q.Filters {
		op := f.Op
		if op == "" {
			op = driver.OpEqual
		}
		typ, e := colType(f.Source, f.Column)
		if e != nil {
			return "", e
		}
		if typ == driver.TypeJSON {
			return "", errorsUnsupportedType(typ)
		}
		switch op {
		case "", driver.OpEqual, driver.OpNotEqual, driver.OpGT, driver.OpGTE, driver.OpLT, driver.OpLTE, driver.OpMatch:
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
	filters := make([]driver.QueryFilter, len(q.Filters))
	copy(filters, q.Filters)
	for i := range filters {
		typ, _ := colType(filters[i].Source, filters[i].Column)
		if typ == driver.TypeBinary || typ == driver.TypeFile {
			filters[i].Values = make([]any, len(q.Filters[i].Values))
			for j, value := range q.Filters[i].Values {
				data, err := driver.FileBytes(value)
				if err != nil {
					return "", err
				}
				filters[i].Values[j] = data
			}
		}
	}
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
		return targetID, t.state.Tables[join.Table.Name][targetID], nil
	}
	value := func(id uint64, source, name string) (any, error) {
		col := columns[columnKey{source, name}]
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
			typ, _ := colType(f.Source, f.Column)
			op := f.Op
			if op == "" {
				op = driver.OpEqual
			}
			anyMatch := op == driver.OpNotEqual
			for _, target := range f.Values {
				v, err := value(id, f.Source, f.Column)
				if err != nil {
					return "", err
				}
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
		for _, item := range order {
			name := item.Column
			typ, _ := colType(item.Source, name)
			left, e := value(ids[i], item.Source, name)
			if e != nil {
				sortErr = e
				return ids[i] < ids[j]
			}
			right, e := value(ids[j], item.Source, name)
			if e != nil {
				sortErr = e
				return ids[i] < ids[j]
			}
			cmp, e := compare(left, right, typ)
			if e != nil {
				sortErr = e
				return ids[i] < ids[j]
			}
			if cmp != 0 {
				if item.Desc {
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
