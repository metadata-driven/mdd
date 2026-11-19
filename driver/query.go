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
// Values supplied by MDD use uint64 for IDs, int64 for integers, bool, string,
// time.Time for datetimes, and []byte for binary and file fields. Decimal
// fields use decimal strings. JSON values use decoded JSON representations;
// support for JSON filters depends on the driver and operator. File filter
// inputs are read into bytes before reaching the driver.
//
// OpContainsAll requires a JSON array column and at least one scalar value.
// MDD supplies uint64 for references and IDs, int64 for integers, json.Number
// for decimals, and string for strings, RFC3339 datetimes, and base64 binary data.
// A nil binary element is represented by nil.
// Callers constructing driver queries should use normalized values. Drivers
// decide which operators and values they support.
type QueryFilter struct {
	Source string `json:"source,omitempty"`
	Column string `json:"column"`
	Op     string `json:"op"`
	Values []any  `json:"values"`
}

const (
	// OpEqual matches any supplied value.
	OpEqual = "="
	// OpNotEqual excludes all supplied values.
	OpNotEqual = "!="
	// OpGT matches values greater than the supplied value.
	OpGT = ">"
	// OpGTE matches values greater than or equal to the supplied value.
	OpGTE = ">="
	// OpLT matches values less than the supplied value.
	OpLT = "<"
	// OpLTE matches values less than or equal to the supplied value.
	OpLTE = "<="
	// OpMatch requests a text match, subject to driver support.
	OpMatch = "~"
	// OpContainsAll matches collections containing every supplied value.
	OpContainsAll = "&="
)
