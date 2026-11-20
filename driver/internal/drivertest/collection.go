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
	assertFilterIDs := func(t *testing.T, filters []driver.QueryFilter, want []uint64) {
		t.Helper()
		q := &driver.Query{Table: table, Filters: filters}
		var got []uint64
		_, err := tx.Fetch(q, func(id uint64) error { got = append(got, id); return nil })
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("got %v, %v; want %v", got, err, want)
		}
	}
	for _, tc := range []struct {
		name   string
		op     string
		values []any
		want   []uint64
	}{
		{"all", driver.OpContainsAll, []any{"read", "write"}, ids[:1]},
		{"duplicate candidates", driver.OpContainsAll, []any{"read", "read"}, ids[:1]},
		{"quoted", driver.OpContainsAll, []any{"quoted\"value"}, ids[:1]},
		{"empty string", driver.OpContainsAll, []any{""}, ids[:1]},
		{"unicode and newline", driver.OpContainsAll, []any{"😀\nline"}, ids[:1]},
		{"case sensitive", driver.OpContainsAll, []any{"READ"}, nil},
		{"partial string", driver.OpContainsAll, []any{"rea"}, nil},
		{"missing", driver.OpContainsAll, []any{"read", "missing"}, nil},
		{"large number", driver.OpContainsAll, []any{json.Number("9007199254740993")}, ids[:1]},
		{"other number", driver.OpContainsAll, []any{json.Number("9007199254740992")}, ids[1:2]},
		{"number as string", driver.OpContainsAll, []any{"9007199254740993"}, nil},
		{"decimal", driver.OpContainsAll, []any{json.Number("1.250")}, ids[:1]},
		{"any, separate rows", driver.OpContainsAny, []any{"read", "bread"}, ids[:2]},
		{"any, first candidate matches", driver.OpContainsAny, []any{"read", "missing"}, ids[:1]},
		{"any, last candidate matches", driver.OpContainsAny, []any{"missing", "read"}, ids[:1]},
		{"any, duplicate candidates", driver.OpContainsAny, []any{"read", "read"}, ids[:1]},
		{"any, no exact match", driver.OpContainsAny, []any{"rea", "READ", "9007199254740993"}, nil},
		{"any, large number", driver.OpContainsAny, []any{json.Number("9007199254740993"), "missing"}, ids[:1]},
		{"any, single candidate", driver.OpContainsAny, []any{"quoted\"value"}, ids[:1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFilterIDs(t, []driver.QueryFilter{{Column: "items", Op: tc.op, Values: tc.values}}, tc.want)
		})
	}
	t.Run("any with another filter", func(t *testing.T) {
		assertFilterIDs(t, []driver.QueryFilter{
			{Column: "items", Op: driver.OpContainsAny, Values: []any{"read", "bread"}},
			{Column: "id", Op: driver.OpEqual, Values: []any{ids[1]}},
		}, ids[1:2])
	})
	for _, filter := range []driver.QueryFilter{
		{Column: "name", Op: driver.OpContainsAll, Values: []any{"read"}},
		{Column: "name", Op: driver.OpContainsAny, Values: []any{"read"}},
		{Column: "items", Op: driver.OpContainsAll},
		{Column: "items", Op: driver.OpContainsAny},
	} {
		_, err := tx.Fetch(&driver.Query{Table: table, Filters: []driver.QueryFilter{filter}}, func(uint64) error { return nil })
		if err == nil {
			t.Errorf("expected error for %#v", filter)
		}
	}
}
