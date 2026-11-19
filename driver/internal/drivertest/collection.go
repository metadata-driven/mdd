package drivertest

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/metadata-driven/mdd/driver"
)

// CollectionFilters checks collection membership through the driver API.
func CollectionFilters(t *testing.T, connection, table string) {
	t.Helper()
	db := &driver.Database{Connection: connection, Tables: []*driver.Table{{
		Name:    table,
		Columns: []*driver.Column{{Name: "items", Type: driver.TypeJSON}, {Name: "name", Type: driver.TypeString}},
	}}}
	conn, err := driver.NewConnection(db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.OpenTransaction(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows := [][]any{
		{"read", "write", "quoted\"value", "", "😀\nline", json.Number("9007199254740993"), json.Number("1.25")},
		{"bread", "read-only", json.Number("9007199254740992")},
		{},
	}
	var ids []uint64
	for _, row := range rows {
		id, err := tx.Set(table, 0, driver.DataObject{"items": row})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for _, tc := range []struct {
		name   string
		values []any
		want   []uint64
	}{
		{"all", []any{"read", "write"}, ids[:1]},
		{"duplicate candidates", []any{"read", "read"}, ids[:1]},
		{"quoted", []any{"quoted\"value"}, ids[:1]},
		{"empty string", []any{""}, ids[:1]},
		{"unicode and newline", []any{"😀\nline"}, ids[:1]},
		{"case sensitive", []any{"READ"}, nil},
		{"partial string", []any{"rea"}, nil},
		{"missing", []any{"read", "missing"}, nil},
		{"large number", []any{json.Number("9007199254740993")}, ids[:1]},
		{"other number", []any{json.Number("9007199254740992")}, ids[1:2]},
		{"number as string", []any{"9007199254740993"}, nil},
		{"decimal", []any{json.Number("1.250")}, ids[:1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &driver.Query{Table: table, Filters: []driver.QueryFilter{{Column: "items", Op: driver.OpContainsAll, Values: tc.values}}}
			var got []uint64
			_, err := tx.Fetch(q, func(id uint64) error { got = append(got, id); return nil })
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	for _, filter := range []driver.QueryFilter{
		{Column: "name", Op: driver.OpContainsAll, Values: []any{"read"}},
	} {
		_, err := tx.Fetch(&driver.Query{Table: table, Filters: []driver.QueryFilter{filter}}, func(uint64) error { return nil })
		if err == nil {
			t.Errorf("expected error for %#v", filter)
		}
	}
}
