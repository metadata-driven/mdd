// Package sqlquery renders driver query plans and reads SQL query results.
package sqlquery

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/driver/internal/query"
)

// Column qualifies a column with its generated source alias.
func Column(c query.Column, quote func(string) string) string {
	return quote(fmt.Sprintf("j%d", c.Source)) + "." + quote(c.Name)
}

// From builds the FROM clause and all left joins with generated aliases.
// SQL filters use j0 as the stable alias for the primary table.
func From(p *query.Plan, quote func(string) string) string {
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

// AppendSQLFilters adds parenthesized predicates in SQL binding order.
func AppendSQLFilters(where []string, args []any, filters []driver.SQLExpression) ([]string, []any, error) {
	for _, filter := range filters {
		if strings.TrimSpace(filter.Text) == "" {
			return nil, nil, errors.New("empty SQL predicate")
		}
		where = append(where, "("+filter.Text+"\n)")
		args = append(args, filter.Args...)
	}
	return where, args, nil
}

// ScanIDs delivers a single column of nonzero IDs and closes the rows.
func ScanIDs(rows *sql.Rows, fn func(uint64) error) error {
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	if len(columns) != 1 {
		return errors.New("SQL query must return one ID column")
	}
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if id == 0 {
			return errors.New("SQL query returned a zero ID")
		}
		if err := fn(id); err != nil {
			if driver.IsBreak(err) {
				return rows.Close()
			}
			return err
		}
	}
	return rows.Err()
}
