package driver

// Query describes a selection of record IDs from a table.
type Query struct {
	Table   string        `json:"table"`
	Index   string        `json:"index,omitempty"`
	Joins   []QueryJoin   `json:"joins,omitempty"`
	Filters []QueryFilter `json:"filters,omitempty"`
	Order   []QueryOrder  `json:"order,omitempty"`
	Limit   int64         `json:"limit,omitempty"`
	Offset  string        `json:"offset,omitempty"`
	Extra   any           `json:"extra,omitempty"`
}

// QueryJoin adds a left join within the same database. Path is a dot-separated
// reference path from the main table, such as "user" or "user.manager". Its
// final segment names a TypeID column of the parent, matched against Table.id.
// A parent join must precede its children, and paths must be unique.
type QueryJoin struct {
	Path  string `json:"path"`
	Table string `json:"table"`
}

// QueryOrder specifies a local column name and its sorting direction. Path
// identifies a declared join, or the main table when empty.
type QueryOrder struct {
	Path   string `json:"path,omitempty"`
	Column string `json:"column"`
	Desc   bool   `json:"desc,omitempty"`
}

// QueryFilter compares a local column with values. Path identifies a declared
// join, or the main table when empty. Filters apply after the left joins,
// so comparisons against a missing joined record do not match.
type QueryFilter struct {
	Path   string `json:"path,omitempty"`
	Column string `json:"column"`
	Op     string `json:"op"`
	Values []any  `json:"values"`
}

const (
	OpEqual    = "="  // equal value
	OpNotEqual = "!=" //
	OpGT       = ">"  //
	OpGTE      = ">=" //
	OpLT       = "<"  //
	OpLTE      = "<=" //
	OpMatch    = "~"  // match text
)

// AddFilter appends a filter on a local column of the main table.
func (q *Query) AddFilter(name, op string, val ...any) {
	q.Filters = append(q.Filters, QueryFilter{
		Column: name,
		Op:     op,
		Values: val,
	})
}
