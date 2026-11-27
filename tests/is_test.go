package tests

import (
	"testing"

	"github.com/metadata-driven/mdd"
)

type comparisonID uint64

func TestObjectIs(t *testing.T) {
	app := NewApp()
	t.Cleanup(func() { _ = app.Close() })

	var alice *User
	var aliceID uint64
	err := app.Execute(nil, func(ctx *Context) {
		alice = ctx.Users().Add().SetLogin("alice")
		aliceID = alice.ID()
		bob := ctx.Users().Add().SetLogin("bob")
		bobID := bob.ID()

		assertEqual(t, alice.Is(alice), true)
		assertEqual(t, alice.Is(ctx.Users().Get(aliceID)), true)
		assertEqual(t, alice.Is(bob), false)
		assertEqual(t, alice.Is(bob, bobID, aliceID), true)
		assertEqual(t, alice.Is(comparisonID(aliceID)), true)
		assertEqual(t, alice.Is(nil, 0, -1, "alice", []int{1}, map[string]int{"id": 1}), false)
		assertEqual(t, mdd.Is(alice, bob, alice), true)

		assertEqual(t, alice.Is(ctx.Orders().Get(aliceID)), false)

		assertEqual(t, ctx.Users().Get(UserSuperAdmin.ID()).Is(UserSuperAdmin), true)
		assertEqual(t, alice.Is(UserSuperAdmin), false)
	})
	if err != nil {
		t.Fatal(err)
	}

	err = app.Execute(nil, func(ctx *Context) {
		assertEqual(t, alice.Is(ctx.Users().Get(aliceID)), true)
	})
	if err != nil {
		t.Fatal(err)
	}

	otherApp := NewApp()
	t.Cleanup(func() { _ = otherApp.Close() })
	err = otherApp.Execute(nil, func(ctx *Context) {
		assertEqual(t, alice.Is(ctx.Users().Get(aliceID)), false)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnumIs(t *testing.T) {
	app := NewApp()
	t.Cleanup(func() { _ = app.Close() })

	err := app.Execute(nil, func(ctx *Context) {
		user := ctx.Users().Add().SetLogin("alice").SetType(UserTypeAdmin)
		kind := user.Type()

		assertEqual(t, kind.Is(UserTypeAdmin), true)
		assertEqual(t, kind.Is(UserTypeGuest, UserTypeAdmin), true)
		assertEqual(t, kind.Is(UserTypeGuest), false)
		assertEqual(t, kind.Is(UserTypeAdmin.ID()), true)
		assertEqual(t, kind.Is(comparisonID(UserTypeAdmin.ID())), true)
		assertEqual(t, kind.Is(OrderStatusDraft), false)
		assertEqual(t, kind.Is(nil, 0, -1, "admin"), false)
		assertEqual(t, mdd.Is(kind, UserTypeGuest, UserTypeAdmin), true)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIsNil(t *testing.T) {
	var user *User
	var kind *UserType
	var object mdd.Object
	var item mdd.EnumItem

	assertEqual(t, mdd.Is(user, UserSuperAdmin), false)
	assertEqual(t, mdd.Is(kind, UserTypeAdmin), false)
	assertEqual(t, mdd.Is(object, 1), false)
	assertEqual(t, mdd.Is(item, UserTypeAdmin), false)
	assertEqual(t, mdd.Is(UserSuperAdmin, user), false)
	assertEqual(t, mdd.Is(UserTypeAdmin, kind), false)
}
