// Package query resolves reference joins and column paths for storage drivers.
package query

import (
	"fmt"
	"strings"

	"github.com/metadata-driven/mdd/driver"
)

// Source is the main table or a left join to a previously resolved source.
type Source struct {
	Table  *driver.Table
	Parent int
	Key    string
}

// Column identifies a column and the source that supplies it.
type Column struct {
	*driver.Column
	Source int
}

// Plan contains sources in join order and the main table's optional index.
type Plan struct {
	Sources []Source
	Index   *driver.Index
	paths   map[string]int
}

// Resolve validates every join, including joins unused by filters or ordering.
func Resolve(database *driver.Database, q *driver.Query) (*Plan, error) {
	table, index, err := database.ResolveQuery(q.Table, q.Index)
	if err != nil {
		return nil, err
	}
	p := &Plan{
		Sources: []Source{{Table: table}},
		Index:   index,
		paths:   map[string]int{"": 0},
	}
	for _, join := range q.Joins {
		if _, exists := p.paths[join.Path]; exists {
			return nil, fmt.Errorf("duplicate or empty join path %q", join.Path)
		}
		parentPath, name := splitColumn(join.Path)
		parent, exists := p.paths[parentPath]
		if !exists || name == "" || strings.HasPrefix(join.Path, ".") {
			return nil, fmt.Errorf("invalid join path %q: parent must be declared first", join.Path)
		}
		col := tableColumn(p.Sources[parent].Table, name)
		if col == nil || col.Type != driver.TypeID {
			return nil, fmt.Errorf("join path %q must reference an ID column", join.Path)
		}
		target := database.TableByName(join.Table)
		if target == nil {
			return nil, fmt.Errorf("unknown join table %q", join.Table)
		}
		p.paths[join.Path] = len(p.Sources)
		p.Sources = append(p.Sources, Source{Table: target, Parent: parent, Key: name})
	}
	return p, nil
}

// Column resolves a local column name in the main table or a declared join.
func (p *Plan) Column(path, name string) (Column, error) {
	source, exists := p.paths[path]
	if !exists {
		return Column{}, fmt.Errorf("unknown query path %q", path)
	}
	if col := tableColumn(p.Sources[source].Table, name); col != nil {
		return Column{Column: col, Source: source}, nil
	}
	return Column{}, fmt.Errorf("unknown query column %q at path %q", name, path)
}

func splitColumn(path string) (string, string) {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[:i], path[i+1:]
	}
	return "", path
}

func tableColumn(table *driver.Table, name string) *driver.Column {
	if name == "id" {
		return &driver.Column{Name: "id", Type: driver.TypeID}
	}
	return table.ColumnByName(name)
}

// SQL qualifies a column with its generated source alias.
func (c Column) SQL(quote func(string) string) string {
	return quote(fmt.Sprintf("j%d", c.Source)) + "." + quote(c.Name)
}

// FromSQL builds the FROM clause and all left joins with generated aliases.
func (p *Plan) FromSQL(quote func(string) string) string {
	var sql strings.Builder
	sql.WriteString(quote(p.Sources[0].Table.Name) + " AS " + quote("j0"))
	for i, source := range p.Sources[1:] {
		alias := quote(fmt.Sprintf("j%d", i+1))
		parent := quote(fmt.Sprintf("j%d", source.Parent))
		sql.WriteString(" LEFT JOIN " + quote(source.Table.Name) + " AS " + alias)
		sql.WriteString(" ON " + parent + "." + quote(source.Key) + "=" + alias + "." + quote("id"))
	}
	return sql.String()
}
