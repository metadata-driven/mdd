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

// QueryJoin adds a left join within the same database. Source identifies a
// previously declared alias, or the main table when empty. Its TypeID Column
// is matched against Table.id. Alias is required and must be unique and nonempty.
// Aliases are opaque source names; SQL drivers generate their own SQL aliases.
type QueryJoin struct {
	Source string `json:"source,omitempty"`
	Column string `json:"column"`
	Table  string `json:"table"`
	Alias  string `json:"alias"`
}

// QueryOrder specifies a local column name and its sorting direction. Source
// identifies a declared alias, or the main table when empty.
type QueryOrder struct {
	Source string `json:"source,omitempty"`
	Column string `json:"column"`
	Desc   bool   `json:"desc,omitempty"`
}

// QueryFilter compares a local column with values. Source identifies a declared
// alias, or the main table when empty. Filters apply after the left joins,
// so comparisons against a missing joined record do not match.
type QueryFilter struct {
	Source string `json:"source,omitempty"`
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
