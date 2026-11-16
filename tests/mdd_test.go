package tests

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/metadata-driven/mdd"
)

func TestApp_RunTest(t *testing.T) {

	app := NewApp()
	app.initTestData()
	app.RunTest(t)
}

func (app *App) RunTest(t *testing.T) {
	t.Helper()
	err := app.Execute(nil, func(ctx *Context) {

		user1 := ctx.UserByID(2)

		assertEqual(t, reflect.TypeOf(user1), reflect.TypeFor[*User]())
		assertEqual(t, user1.ID(), uint64(2))
		assertEqual(t, user1.Login(), "alice")
		assertEqual(t, user1.String(), "@alice (Alice)")
		assertEqual(t, user1, ctx.UserByID(2))
		assertEqual(t, user1.Context(), ctx)
		assertEqual(t, user1.Context().App(), app)
		assertEqual(t, ctx.App(), app)

		user2 := ctx.UserByID(3)
		assertEqual(t, user2.Login(), "bob")
		assertEqual(t, user2.String(), "@bob (Bob)")
		assertEqual(t, user2.Context(), user1.Context())
		assertEqual(t, user2.Name(), "Bob")

		users := ctx.Users()
		assertEqual(t, reflect.TypeOf(users), reflect.TypeFor[*mdd.Set[User]]())
		assertEqual(t, user1, users.Get(2))
		assertEqual(t, user2, users.Get(3))
		assertEqual(t, users.Get(3).Name(), "Bob")

		require(t, len(user1.Orders().IDs()) > 0)
		user1.RecentOrders().Fetch(func(o *Order) {
			t.Logf("order: %s", o)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (app *App) initTestData() {
	noerr(app.Execute(nil, func(ctx *Context) {
		alice := ctx.Users().Add().SetLogin("alice")
		bob := ctx.Users().Add().SetLogin("bob").SetManager(alice)

		ctx.Orders().Add().SetUser(alice).SetAmount(101.01).SetStatus(1)
		ctx.Orders().Add().SetUser(alice).SetAmount(102.02).SetStatus(2)
		ctx.Orders().Add().SetUser(alice).SetAmount(103.03).SetStatus(2)
		ctx.Orders().Add().SetUser(alice).SetAmount(104.04).SetStatus(3)

		ctx.Orders().Add().SetUser(bob).SetAmount(200.00)
		ctx.Orders().Add().SetUser(bob).SetAmount(201.00)
		ctx.Orders().Add().SetUser(bob).SetAmount(202.00)
		ctx.Orders().Add().SetUser(bob).SetAmount(203.00)
		ctx.Orders().Add().SetUser(bob).SetAmount(204.00)
		ctx.Orders().Add().SetUser(bob).SetAmount(205.00)
	}))
}

func noerr(err error) {
	if err != nil {
		panic(err)
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func require(t *testing.T, ok bool) {
	t.Helper()
	if !ok {
		t.Error()
	}
}

func TestBinaryAndFileAttributes(t *testing.T) {
	app := NewApp()
	var id uint64
	err := app.Execute(nil, func(ctx *Context) {
		user := ctx.Users().Add().SetLogin("binary-user").SetToken([]byte{0, 1, 255}).SetAvatar(strings.NewReader("avatar"))
		id = user.ID()
		if got := user.Token(); !bytes.Equal(got, []byte{0, 1, 255}) {
			t.Fatalf("token after flush = %v", got)
		}
		avatar := user.Avatar()
		if avatar == nil {
			t.Fatal("missing avatar")
		}
		if got, err := io.ReadAll(avatar); err != nil || string(got) != "avatar" {
			t.Fatalf("avatar = %q, %v", got, err)
		}
		user.SetToken([]byte{})
		if user.Token() != nil {
			t.Fatal("empty pending binary should be nil")
		}
		user.SetToken([]byte("updated"))
		if got := user.Token(); !bytes.Equal(got, []byte("updated")) {
			t.Fatalf("pending token = %q", got)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	err = app.Execute(nil, func(ctx *Context) {
		user := ctx.UserByID(id)
		if got := user.Token(); !bytes.Equal(got, []byte("updated")) {
			t.Fatalf("saved token = %q", got)
		}
		if got, err := io.ReadAll(user.Avatar()); err != nil || string(got) != "avatar" {
			t.Fatalf("saved avatar = %q, %v", got, err)
		}
		user.SetToken(nil).SetAvatar(nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = app.Execute(nil, func(ctx *Context) {
		user := ctx.UserByID(id)
		if user.Token() != nil || user.Avatar() != nil {
			t.Fatal("empty values should be nil")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}
