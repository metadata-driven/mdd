package tests

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/metadata-driven/mdd"
)

func (ctx *Context) QueryStatus() string {
	return ctx.Users().Filter("login", "alice").MustFind().Name()
}

func TestAttributeQueries(t *testing.T) {
	app := NewApp()
	err := app.Execute(nil, func(ctx *Context) {
		alice := ctx.Users().Add().SetLogin("alice")
		bob := ctx.Users().Add().SetLogin("bob")
		base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

		var placed []uint64
		for i := range 4 {
			placed = append(placed, ctx.Orders().Add().SetUser(alice).SetTs(base.Add(time.Duration(i)*time.Hour)).SetStatus(2).ID())
		}
		draftID := ctx.Orders().Add().SetUser(alice).SetTs(base.Add(4 * time.Hour)).SetStatus(1).ID()
		otherID := ctx.Orders().Add().SetUser(bob).SetTs(base).SetStatus(2).ID()
		otherDraftID := ctx.Orders().Add().SetUser(bob).SetTs(base.Add(time.Hour)).SetStatus(1).ID()

		wantOrders := append(slices.Clone(placed), draftID)
		assertIDs(t, alice.Orders().IDs(), wantOrders)
		assertIDs(t, bob.Orders().IDs(), []uint64{otherID, otherDraftID})
		assertIDs(t, alice.RecentOrders().IDs(), []uint64{placed[3], placed[2], placed[1]})
		assertIDs(t, alice.PreferredOrders().IDs(), placed)
		assertIDs(t, bob.PreferredOrders().IDs(), []uint64{otherDraftID})
		assertEqual(t, alice.Description(), "placed")
		assertEqual(t, bob.Icon(), "")

		// Each attribute call must return an independent set.
		assertIDs(t, alice.Orders().Limit(1).IDs(), placed[:1])
		assertIDs(t, alice.Orders().IDs(), wantOrders)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSetQuery(t *testing.T) {
	app := NewApp()
	err := app.Execute(nil, func(ctx *Context) {
		alice := ctx.Users().Add().SetLogin("alice").SetName("placed")
		bob := ctx.Users().Add().SetLogin("bob")

		orderID1 := ctx.Orders().Add().SetUser(alice).SetAmount(10).SetStatus(2).ID()
		orderID2 := ctx.Orders().Add().SetUser(alice).SetAmount(20).SetStatus(2).ID()
		orderID3 := ctx.Orders().Add().SetUser(bob).SetAmount(30).SetStatus(2).ID()
		draftID := ctx.Orders().Add().SetUser(alice).SetAmount(40).SetStatus(1).ID()

		filtered := ctx.Orders().Filter("user", alice).Query("where(status=placed) order(amount desc) limit(1)")
		assertIDs(t, filtered.IDs(), []uint64{orderID2})
		assertIDs(t, mdd.NewQuery[Order](ctx, "from(orders) where(status=placed) order(amount)").IDs(), []uint64{orderID1, orderID2, orderID3})
		assertIDs(t, ctx.Orders().Query("where(status={QueryStatus}) order(amount desc)").IDs(), []uint64{orderID3, orderID2, orderID1})
		alice.SetName("draft")
		assertIDs(t, ctx.Orders().Query("where(status={QueryStatus}) order(amount desc)").IDs(), []uint64{draftID})
		assertIDs(t, ctx.Orders().Order("user").Limit(1).Query("where(status=placed) order(amount desc) limit(3)").IDs(), []uint64{orderID2, orderID1, orderID3})

		// Reusing a cached expression must not reuse another set's filters or limit.
		query := "where(status=placed) order(amount)"
		assertIDs(t, ctx.Orders().Query(query).Filter("user", alice).Limit(1).IDs(), []uint64{orderID1})
		assertIDs(t, ctx.Orders().Query(query).IDs(), []uint64{orderID1, orderID2, orderID3})
		assertEqual(t, ctx.Users().Query("where(id > 0)").Count(), int64(3))
		assertEqual(t, ctx.Orders().Query("where(id > 0)").Count(), int64(4))
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSetQueryErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{"other source", "from(users)", "does not match"},
		{"unknown source", "from(missing)", "unknown query source"},
		{"unknown field", "where(missing=1)", "unknown query field"},
		{"select", "select(id)", "select is not supported"},
		{"invalid syntax", "where(", "xquery:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := NewApp()
			err := app.Execute(nil, func(ctx *Context) { ctx.Orders().Query(tc.query) })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("query %q: got error %v, want %q", tc.query, err, tc.want)
			}
		})
	}
}

func assertIDs(t *testing.T, got, want []uint64) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("got IDs %v, want %v", got, want)
	}
}
