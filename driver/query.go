package driver

// Query describes a selection of record IDs from a table.
type Query struct {
	Table   string        `json:"table"`
	Index   string        `json:"index,omitempty"`
	Filters []QueryFilter `json:"filters,omitempty"`
	Order   []QueryOrder  `json:"order,omitempty"`
	Limit   int64         `json:"limit,omitempty"`
	Offset  string        `json:"offset,omitempty"`
	Extra   any           `json:"extra,omitempty"`
}

// QueryOrder specifies a column and its sorting direction.
type QueryOrder struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc,omitempty"`
}

// QueryFilter specifies a comparison between a column and values.
type QueryFilter struct {
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

func (q *Query) AddFilter(name, op string, val ...any) {
	q.Filters = append(q.Filters, QueryFilter{
		Column: name,
		Op:     op,
		Values: val,
	})
}
