package xquery

import (
	"encoding/json"
	"log"
	"reflect"
	"strings"
	"testing"
)

func TestParseExample(t *testing.T) {
	const src = `
		# Orders
		from(billing/orders)
		select(icon, ts, status, user.name, amount,)
		where(status = done, canceled)
		where(user.type not in ["guest", moderator])
		where(amount > 1)
		order(user.type asc, ts desc,)
		limit(100)
		offset(200)
	`
	q, err := Parse(src)
	require(t, err == nil, "Parse returned an error: ", err)
	assertEqual(t, q.From, "billing/orders")
	assertEqual(t, q.FromParts(), []string{"billing", "orders"})
	assertEqual(t, len(q.Select), 5)
	assertEqual(t, q.Select[3], Field("user.name"))
	assertEqual(t, q.Select[3].Parts(), []string{"user", "name"})
	assertEqual(t, len(q.Where), 3)
	assertEqual(t, q.Where[0].Op, "=")
	assertEqual(t, q.Where[0].Values, []any{"done", "canceled"})
	assertEqual(t, q.Where[1].Op, "!=")
	assertEqual(t, len(q.Order), 2)
	assertEqual(t, q.Order[0].Desc, false)
	assertEqual(t, q.Order[1].Desc, true)
	assertEqual(t, q.Limit, int64(100))
	assertEqual(t, q.Offset, int64(200))
}

func TestCommentsInsideDirectivesAndStrings(t *testing.T) {
	q, err := Parse(`
		from(users) select(
			id, // identifier
			url, # URL
		) where(url = "https://example.com/a//b") where(tag = "#important")
	`)
	require(t, err == nil, "Parse returned an error: ", err)
	assertEqual(t, q.Where[0].Values[0], "https://example.com/a//b")
	assertEqual(t, q.Where[1].Values[0], "#important")
}

func TestEmptySetsAreParsed(t *testing.T) {
	q, err := Parse(`from(users) where(id != []) where(id = [])`)
	require(t, err == nil, "Parse returned an error: ", err)
	assertEqual(t, q.IsEmpty(), true)
	assertEqual(t, len(q.Where), 2)
	assertEqual(t, q.Where[0], Condition{Field: Field("id"), Op: "!=", Values: nil})
	assertEqual(t, q.Where[1], Condition{Field: Field("id"), Op: "=", Values: nil})
}

func TestJSONValues(t *testing.T) {
	q, err := Parse(`from(users) where(data = [1, -2, 1.5, true, false, null, "x"])`)
	require(t, err == nil, "Parse returned an error: ", err)
	assertEqual(t, toJSON(q), `{"from":"users","where":[{"field":"data","op":"=","values":[1,-2,1.5,true,false,null,"x"]}]}`)

	want := []any{int64(1), int64(-2), 1.5, true, false, nil, "x"}
	assertEqual(t, q.Where[0].Values, want)
}

func TestOffsetString(t *testing.T) {
	q, err := Parse(`offset("next-page") from(users)`)
	require(t, err == nil, "Parse returned an error: ", err)
	assertEqual(t, q.Offset, "next-page")
}

func TestFromArgument(t *testing.T) {
	q, err := Parse(`select(id) where(active = true)`, Args{"from": "billing/users"})
	require(t, err == nil, "Parse returned an error: ", err)
	assertEqual(t, q.From, "billing/users")
	assertEqual(t, q.String(), `from(billing/users) select(id) where(active = true)`)
}

func TestExplicitFromTakesPrecedenceOverArgument(t *testing.T) {
	q, err := Parse(`from(users) select(id)`, Args{"from": "invalid//fallback"})
	require(t, err == nil, "Parse returned an error: ", err)
	assertEqual(t, q.From, "users")
}

func TestFullTextSearch(t *testing.T) {
	q, err := Parse(`from(orders) where(description ~ "canceled by client request")`)
	require(t, err == nil, "Parse returned an error: ", err)
	assertEqual(t, q.Where, []Condition{{
		Field:  "description",
		Op:     "~",
		Values: []any{"canceled by client request"},
	}})
	assertEqual(t, q.String(), `from(orders) where(description ~ "canceled by client request")`)
}

func TestEscapedStringAndFlattenedNestedArray(t *testing.T) {
	q, err := Parse(`from(orders) where(description ~ "Отменён по \"запросу\" клиента") where(matrix = [[1, 2], [3, [4, 5]]])`)
	require(t, err == nil, "Parse returned an error: ", err)
	assertEqual(t, q.Where[0].Values, []any{`Отменён по "запросу" клиента`})
	assertEqual(t, q.Where[1].Values, []any{int64(1), int64(2), int64(3), int64(4), int64(5)})
	assertEqual(t, q.String(), `from(orders) where(description ~ "Отменён по \"запросу\" клиента") where(matrix = 1, 2, 3, 4, 5)`)

	q2, err := Parse(q.String())
	require(t, err == nil, "Round-trip parse returned an error: ", err)
	assertEqual(t, q2, q)
}

func TestQueryJSON(t *testing.T) {
	q, err := Parse(`from(billing/orders) select(id, user.name) where(status = done) order(ts desc) limit(10) offset("next")`)
	require(t, err == nil, "Parse returned an error: ", err)

	assertEqual(t, toJSON(q), `{"from":"billing/orders","select":["id","user.name"],"where":[{"field":"status","op":"=","values":["done"]}],"order":[{"field":"ts","desc":true}],"limit":10,"offset":"next"}`)
}

func TestQueryJSONDoesNotExposeEmptyState(t *testing.T) {
	q, err := Parse(`from(users) where(id = [])`)
	require(t, err == nil, "Parse returned an error: ", err)
	require(t, q.IsEmpty(), "Query must be empty")
}

func TestQueryStringRoundTrip(t *testing.T) {
	const src = `
		from(billing/orders)
		select(id, user.name)
		where(status = done, canceled)
		where(amount >= 12.5)
		where(parent != null)
		order(user.name, ts desc)
		limit(10)
		offset("next-page")
	`
	q, err := Parse(src)
	require(t, err == nil, "Parse returned an error: ", err)
	assertEqual(t, q.String(), `from(billing/orders) select(id, user.name) where(status = "done", "canceled") where(amount >= 12.5) where(parent != null) order(user.name, ts desc) limit(10) offset("next-page")`)

	q2, err := Parse(q.String())
	require(t, err == nil, "Round-trip parse returned an error: ", err)
	assertEqual(t, q2, q)
}

func TestEmptyQueryStringRoundTrip(t *testing.T) {
	want, err := Parse(`from(users) where(id = [])`)
	require(t, err == nil, "Parse returned an error: ", err)
	assertEqual(t, want.String(), `from(users) where(id = [])`)

	actual, err := Parse(want.String())
	require(t, err == nil, "Round-trip parse returned an error: ", err)
	assertEqual(t, actual, want)
}

func TestErrors(t *testing.T) {
	tests := []struct{ name, src, contains string }{
		{"missing from", `select(id)`, "from is required"},
		{"duplicate from", `from(a) from(b)`, "only once"},
		{"duplicate limit", `from(a) limit(1) limit(2)`, "only once"},
		{"multiple ordered values", `from(a) where(n > 1, 2)`, "exactly one"},
		{"multiple full-text values", `from(a) where(description ~ "one", "two")`, "exactly one"},
		{"zero limit", `from(a) limit(0)`, "positive integer"},
		{"fractional limit", `from(a) limit(1.5)`, "positive integer"},
		{"unknown directive", `from(a) group(id)`, "unknown directive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.src)
			require(t, err != nil, "Parse unexpectedly succeeded")
			require(t, strings.Contains(err.Error(), tt.contains), "Unexpected error: ", err)
		})
	}
}

func TestArgumentErrors(t *testing.T) {
	tests := []struct {
		name string
		args []Args
	}{
		{"missing from", nil},
		{"empty from", []Args{{"from": ""}}},
		{"invalid from", []Args{{"from": "billing//users"}}},
		{"multiple argument sets", []Args{{"from": "users"}, {"from": "orders"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(`select(id)`, tt.args...)
			require(t, err != nil, "Parse unexpectedly succeeded")
		})
	}
}

func require(t *testing.T, ok bool, msg ...any) {
	t.Helper()
	if !ok {
		t.Fatal(msg...)
	}
}

func assertEqual(t *testing.T, actual, expected any) {
	t.Helper()
	require(t, reflect.DeepEqual(actual, expected), "Values are not equal: actual=", actual, ", expected=", expected)
}

func toJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		log.Panicf("json.Marshal returned an error: %v", err)
	}
	return string(data)
}

func TestCollectionOperator(t *testing.T) {
	q, err := Parse("from(users) where(permissions &= manage_users, manage_orders)")
	require(t, err == nil, err)
	assertEqual(t, q.Where[0].Op, "&=")
	assertEqual(t, q.Where[0].Values, []any{"manage_users", "manage_orders"})
	for _, src := range []string{"where(tags &= [])", "where(tags & read)"} {
		_, err := Parse(src)
		require(t, err != nil, "expected error for ", src)
	}
}
