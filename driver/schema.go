// Package dbschema describes database-independent storage schemas.
// MDD configuration generators produce these structures, and database drivers
// translate them into database-specific definitions.
package driver

import (
	"fmt"
	"net/url"
	"strings"
)

type (
	// Database contains the tables in a database schema.
	// Connection settings belong to the driver and are not part of the schema.
	Database struct {
		Name    string  `json:"name"`
		Options Options `json:"options,omitempty"`
		Comment string  `json:"comment,omitempty"`

		Connection string `json:"connection"`

		Tables []*Table `json:"tables"`
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
	IndexTypeIndex    IndexType = "index"
	IndexTypeUnique   IndexType = "unique"
	IndexTypeFulltext IndexType = "fulltext"
)

func (t *Database) ConnectionURL() *url.URL {
	p, _ := url.Parse(t.Connection)
	return p
}

func (t *Database) TableByName(name string) *Table {
	for _, tab := range t.Tables {
		if tab.Name == name {
			return tab
		}
	}
	return nil
}

func (t *Table) ColumnByName(name string) *Column {
	// todo: add cache
	for _, col := range t.Columns {
		if col.Name == name {
			return col
		}
	}
	return nil
}

func (t *Table) ColumnsByName(name ...string) []*Column {
	cc := make([]*Column, len(name))
	for i, s := range name {
		cc[i] = t.ColumnByName(s)
	}
	return cc
}

func (t *Table) IndexByName(name string) *Index {
	// todo: add cache
	for _, idx := range t.Indexes {
		if idx.Name == name {
			return idx
		}
	}
	return nil
}

func (t *Table) IndexColumns(idxName string) []*Column {
	// todo: add cache
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

// ResolveQuery resolves the table and optional index named by a query.
func (d *Database) ResolveQuery(tableName, indexName string) (*Table, *Index, error) {
	table := d.TableByName(tableName)
	if table == nil {
		return nil, nil, fmt.Errorf("unknown table %q", tableName)
	}
	if indexName == "" {
		return table, nil, nil
	}
	index := table.IndexByName(indexName)
	if index == nil {
		return nil, nil, fmt.Errorf("unknown index %q on table %q", indexName, tableName)
	}
	return table, index, nil
}

// ResolveQuerySource finds a table or index by name. A table/index path
// identifies an index on a specific table; ambiguous index names are rejected.
func (d *Database) ResolveQuerySource(from string) (*Table, *Index, error) {
	if tableName, indexName, ok := strings.Cut(from, "/"); ok {
		table := d.TableByName(tableName)
		if table == nil {
			return nil, nil, fmt.Errorf("unknown table %q", tableName)
		}
		index := table.IndexByName(indexName)
		if index == nil {
			return nil, nil, fmt.Errorf("unknown index %q on table %q", indexName, tableName)
		}
		return table, index, nil
	}
	if table := d.TableByName(from); table != nil {
		return table, nil, nil
	}
	var foundTable *Table
	var foundIndex *Index
	for _, table := range d.Tables {
		if index := table.IndexByName(from); index != nil {
			if foundIndex != nil {
				return nil, nil, fmt.Errorf("ambiguous index %q", from)
			}
			foundTable, foundIndex = table, index
		}
	}
	if foundIndex == nil {
		return nil, nil, fmt.Errorf("unknown table or index %q", from)
	}
	return foundTable, foundIndex, nil
}
