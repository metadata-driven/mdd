// Package driver defines database-independent storage schemas and driver APIs.
// MDD configuration generators produce these structures, and database drivers
// translate them into database-specific definitions.
package driver

import "net/url"

type (
	// Database contains the tables in a database schema.
	// Connection settings belong to the driver and are not part of the schema.
	Database struct {
		Name    string  `json:"name"`
		Options Options `json:"options,omitempty"`
		Comment string  `json:"comment,omitempty"`

		Connection string `json:"connection"`

		Tables []*Table `json:"tables"`

		tablesByName map[string]*Table
	}

	// Table describes a table and its columns and indexes. Every table has a
	// driver-managed uint64 id as its primary key; id is reserved and must not
	// be declared in Columns.
	Table struct {
		Name    string  `json:"name"`
		Options Options `json:"options,omitempty"`
		Comment string  `json:"comment,omitempty"`

		Columns []*Column `json:"columns"`
		Indexes []*Index  `json:"indexes,omitempty"`

		columnsByName map[string]*Column
		indexesByName map[string]*Index
	}

	// Column describes a stored value.
	Column struct {
		Name    string  `json:"name"`
		Options Options `json:"options,omitempty"`
		Comment string  `json:"comment,omitempty"`

		Type DataType `json:"type"`

		// Length is the maximum character count for TypeString or byte count for TypeBinary.
		// Zero leaves the length unspecified. TypeFile does not use Length.
		Length int `json:"length,omitempty"`
		// Precision is the total number of decimal digits for TypeNumber.
		// Zero leaves the precision unspecified.
		Precision int `json:"precision,omitempty"`
		// Scale is the number of fractional decimal digits for TypeNumber.
		// With an explicit precision, scale must be between zero and precision.
		Scale int `json:"scale,omitempty"`
	}

	// Index describes an ordered set of column names. Unique indexes enforce
	// uniqueness of the complete tuple. Drivers may reject unsupported indexes. Options
	// contains optional driver-specific settings.
	Index struct {
		Name    string    `json:"name"`
		Options Options   `json:"options,omitempty"`
		Type    IndexType `json:"type"`
		Columns []string  `json:"columns"`
	}

	// Options contains arbitrary JSON-compatible driver settings.
	Options = map[string]any

	// IndexType identifies the purpose of an index independently of its SQL syntax.
	IndexType string
)

const (
	// IndexTypeIndex identifies a regular index.
	IndexTypeIndex IndexType = "index"
	// IndexTypeUnique identifies an index enforcing unique tuples.
	IndexTypeUnique IndexType = "unique"
	// IndexTypeFulltext identifies a full-text index.
	IndexTypeFulltext IndexType = "fulltext"
)

// ConnectionURL parses the connection string, returning nil if it is invalid.
func (t *Database) ConnectionURL() *url.URL {
	p, _ := url.Parse(t.Connection)
	return p
}

// TableByName returns the named table, or nil if it is absent.
func (t *Database) TableByName(name string) *Table {
	if t.tablesByName == nil {
		t.tablesByName = make(map[string]*Table, len(t.Tables))
		for _, table := range t.Tables {
			if table != nil && t.tablesByName[table.Name] == nil {
				t.tablesByName[table.Name] = table
			}
		}
	}
	return t.tablesByName[name]
}

// ColumnByName returns the named column, or nil if it is absent.
func (t *Table) ColumnByName(name string) *Column {
	if t.columnsByName == nil {
		t.columnsByName = make(map[string]*Column, len(t.Columns))
		for _, column := range t.Columns {
			if column != nil && t.columnsByName[column.Name] == nil {
				t.columnsByName[column.Name] = column
			}
		}
	}
	return t.columnsByName[name]
}

// ColumnsByName returns columns in the supplied order, with nil for missing names.
func (t *Table) ColumnsByName(name ...string) []*Column {
	cc := make([]*Column, len(name))
	for i, s := range name {
		cc[i] = t.ColumnByName(s)
	}
	return cc
}

// IndexByName returns the named index, or nil if it is absent.
func (t *Table) IndexByName(name string) *Index {
	if t.indexesByName == nil {
		t.indexesByName = make(map[string]*Index, len(t.Indexes))
		for _, index := range t.Indexes {
			if index != nil && t.indexesByName[index.Name] == nil {
				t.indexesByName[index.Name] = index
			}
		}
	}
	return t.indexesByName[name]
}

// IndexColumns returns the index's columns in order, or nil if the index is absent.
// A missing column is represented by nil.
func (t *Table) IndexColumns(idxName string) []*Column {
	idx := t.IndexByName(idxName)
	if idx == nil {
		return nil
	}
	cols := make([]*Column, len(idx.Columns))
	for i, colName := range idx.Columns {
		cols[i] = t.ColumnByName(colName)
	}
	return cols
}
