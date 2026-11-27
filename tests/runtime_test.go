package tests

import (
	"errors"
	"reflect"
	"testing"

	"github.com/metadata-driven/mdd"
	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/driver/mem"
	"github.com/metadata-driven/mdd/meta"
)

func TestNilModelChains(t *testing.T) {
	var user *User
	var ctx *Context

	assertEqual(t, user.Manager().Manager().Login(), "")
	assertEqual(t, user.Manager().ID(), uint64(0))
	assertEqual(t, user.Manager().Context(), (*Context)(nil))
	assertEqual(t, user.Orders().Filter("user", user).First(), (*Order)(nil))
	assertEqual(t, ctx.Users().Order("login").Count(), int64(0))
	assertEqual(t, ctx.Users().Exists(), false)
	assertEqual(t, ctx.App(), (*App)(nil))
	assertEqual(t, user.SetLogin("alice"), (*User)(nil))

	app := NewApp()
	t.Cleanup(func() { _ = app.Close() })
	err := app.Execute(nil, func(ctx *Context) {
		user := ctx.Users().Add().SetLogin("alice")
		assertEqual(t, user.Manager().Manager().Login(), "")
		assertEqual(t, user.Manager().ID(), uint64(0))
	})
	if err != nil {
		t.Fatal(err)
	}
}

type closeTrackingConnection struct {
	driver.Connection
	closed int
	err    error
}

func (c *closeTrackingConnection) Close() error {
	c.closed++
	return errors.Join(c.Connection.Close(), c.err)
}

func TestApplicationClose(t *testing.T) {
	firstFailure := errors.New("first close failed")
	secondFailure := errors.New("second close failed")
	failures := map[string]error{"main": firstFailure, "secondary": secondFailure}
	connections := map[string][]*closeTrackingConnection{}
	err := driver.Register(func(db *driver.Database) (driver.Connection, error) {
		conn, err := mem.NewConnection(db, nil, nil)
		if err != nil {
			return nil, err
		}
		tracked := &closeTrackingConnection{Connection: conn, err: failures[db.Name]}
		connections[db.Name] = append(connections[db.Name], tracked)
		return tracked, nil
	}, "test-app-close")
	if err != nil {
		t.Fatal(err)
	}

	const config = `
name: close-test
sources:
  - name: main
    conn: 'test-app-close:'
  - name: secondary
    conn: 'test-app-close:'
models:
  - name: user
    source: main
    set: users
    attrs:
      - name: login
        type: string
  - name: order
    source: secondary
    set: orders
    attrs:
      - name: amount
        type: number
`
	cfg, err := meta.ParseYAML([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	cfg.LookupModel("user").Register(reflect.TypeFor[User]())
	cfg.LookupModel("order").Register(reflect.TypeFor[Order]())
	app, err := mdd.NewApp[App](cfg)
	if err != nil {
		t.Fatal(err)
	}
	err = app.Execute(nil, func(ctx *Context) {
		ctx.Users().Count()
		ctx.Orders().Count()
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(connections["main"]) != 1 || len(connections["secondary"]) != 1 {
		t.Fatalf("opened connections: main=%d, secondary=%d; want one each", len(connections["main"]), len(connections["secondary"]))
	}
	err = app.Close()
	if !errors.Is(err, firstFailure) || !errors.Is(err, secondFailure) {
		t.Fatalf("Close() = %v, want both connection errors", err)
	}
	assertEqual(t, connections["main"][0].closed, 1)
	assertEqual(t, connections["secondary"][0].closed, 1)
	if err := app.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	assertEqual(t, connections["main"][0].closed, 1)
	assertEqual(t, connections["secondary"][0].closed, 1)
}
