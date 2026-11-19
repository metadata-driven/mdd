package xquery

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Query is the normalized representation of a parsed query.
type Query struct {
	From   string      `json:"from"`
	Select []Field     `json:"select,omitempty"`
	Where  []Condition `json:"where,omitempty"`
	Order  []Order     `json:"order,omitempty"`
	Limit  int64       `json:"limit,omitempty"`
	Offset any         `json:"offset,omitempty"`
}

type Field string

// Condition compares a field with one or more values. Parsed numbers use int64
// or uint64 when they fit, and json.Number for fractions, exponents and larger
// integers.
type Condition struct {
	Field  Field  `json:"field"`
	Op     string `json:"op"`
	Values []any  `json:"values"`
}

type Order struct {
	Field Field `json:"field"`
	Desc  bool  `json:"desc,omitempty"`
}

// AddFilter appends a condition to the query.
func (q *Query) AddFilter(name, op string, values ...any) {
	q.Where = append(q.Where, Condition{Field: Field(name), Op: op, Values: values})
}

// FromParts returns the source components separated by slashes.
func (q *Query) FromParts() []string {
	if q == nil || q.From == "" {
		return nil
	}
	return strings.Split(q.From, "/")
}

// IsEmpty reports whether the query is guaranteed to return no results.
func (q *Query) IsEmpty() bool {
	if q == nil {
		return false
	}
	for _, condition := range q.Where {
		if condition.Op == "=" && len(condition.Values) == 0 {
			return true
		}
	}
	return false
}

// String returns the dot-separated field name.
func (f Field) String() string {
	return string(f)
}

// Parts returns the field components separated by dots.
func (f Field) Parts() []string {
	if f == "" {
		return nil
	}
	return strings.Split(string(f), ".")
}

// String returns the canonical xquery representation of the query.
func (q *Query) String() string {
	if q == nil {
		return ""
	}

	var directives []string
	if len(q.From) > 0 {
		directives = append(directives, "from("+q.From+")")
	}
	if len(q.Select) > 0 {
		fields := make([]string, len(q.Select))
		for i, field := range q.Select {
			fields[i] = field.String()
		}
		directives = append(directives, "select("+strings.Join(fields, ", ")+")")
	}
	for _, condition := range q.Where {
		values := make([]string, len(condition.Values))
		for i, value := range condition.Values {
			values[i] = valueString(value)
		}
		valueList := strings.Join(values, ", ")
		if len(values) == 0 {
			valueList = "[]"
		}
		directives = append(directives, fmt.Sprintf("where(%s %s %s)", condition.Field.String(), condition.Op, valueList))
	}
	if len(q.Order) > 0 {
		items := make([]string, len(q.Order))
		for i, order := range q.Order {
			items[i] = order.Field.String()
			if order.Desc {
				items[i] += " desc"
			}
		}
		directives = append(directives, "order("+strings.Join(items, ", ")+")")
	}
	if q.Limit > 0 {
		directives = append(directives, fmt.Sprintf("limit(%d)", q.Limit))
	}
	if q.Offset != nil {
		directives = append(directives, "offset("+valueString(q.Offset)+")")
	}
	return strings.Join(directives, " ")
}

func valueString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(data)
}
