// Package query resolves reference joins and column sources for storage drivers.
package query

import (
	"fmt"

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
	aliases map[string]int
}

// Resolve validates every join, including joins unused by filters or ordering.
func Resolve(database *driver.Database, q *driver.Query) (*Plan, error) {
	table := database.TableByName(q.Table)
	if table == nil {
		return nil, fmt.Errorf("unknown table %q", q.Table)
	}
	var index *driver.Index
	if q.Index != "" {
		index = table.IndexByName(q.Index)
		if index == nil {
			return nil, fmt.Errorf("unknown index %q on table %q", q.Index, q.Table)
		}
	}
	p := &Plan{
		Sources: []Source{{Table: table}},
		Index:   index,
		aliases: map[string]int{"": 0},
	}
	for _, join := range q.Joins {
		if _, exists := p.aliases[join.Alias]; exists {
			return nil, fmt.Errorf("duplicate or empty join alias %q", join.Alias)
		}
		parent, exists := p.aliases[join.Source]
		if !exists {
			return nil, fmt.Errorf("unknown join source %q: source must be declared first", join.Source)
		}
		col := tableColumn(p.Sources[parent].Table, join.Column)
		if col == nil || col.Type != driver.TypeID {
			return nil, fmt.Errorf("join column %q in source %q must be an ID column", join.Column, join.Source)
		}
		target := database.TableByName(join.Table)
		if target == nil {
			return nil, fmt.Errorf("unknown join table %q", join.Table)
		}
		p.aliases[join.Alias] = len(p.Sources)
		p.Sources = append(p.Sources, Source{Table: target, Parent: parent, Key: join.Column})
	}
	return p, nil
}

// Column resolves a local column name in the main table or a declared join.
func (p *Plan) Column(source, name string) (Column, error) {
	sourceIndex, exists := p.aliases[source]
	if !exists {
		return Column{}, fmt.Errorf("unknown query source %q", source)
	}
	if col := tableColumn(p.Sources[sourceIndex].Table, name); col != nil {
		return Column{Column: col, Source: sourceIndex}, nil
	}
	return Column{}, fmt.Errorf("unknown query column %q in source %q", name, source)
}

func tableColumn(table *driver.Table, name string) *driver.Column {
	if name == "id" {
		return &driver.Column{Name: "id", Type: driver.TypeID}
	}
	return table.ColumnByName(name)
}
