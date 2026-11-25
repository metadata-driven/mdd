# MDD for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/metadata-driven/mdd.svg)](https://pkg.go.dev/github.com/metadata-driven/mdd)

MDD for Go is a framework that implements the [Metadata-Driven Development (MDD) approach](https://github.com/metadata-driven/spec). It provides a runtime for models, transactions, queries, storage, templates, and application logic.

## What is MDD?

Metadata-Driven Development (MDD) is an architectural approach to designing information systems based on metadata. The metadata describes a shared model of the system that serves as the basis for its subsystems and applications.

MDD metadata is typically written in YAML. The shared model gives business teams, developers, administrators, DevOps engineers, and AI agents a common language for discussing entities, attributes, relationships, and operations.

The description goes beyond a physical database schema. It captures logical models and their relationships, presentation details, enumerations, queries, and storage choices. It can serve as the basis for database schemas, application code, editors, forms, and templates. These are uses of the metadata, not a claim that this repository already generates all of them.

The Go framework lets developers express business logic in terms of models and sets while it manages transaction contexts and storage access. The same logical model and application logic can be used with different storage drivers, subject to each driver's and database's capabilities. The [MDD specification](https://github.com/metadata-driven/spec/blob/main/specification.md) defines the metadata format independently of Go and of any particular implementation.

- [What is MDD?](#what-is-mdd)
- [Get started](#get-started)
- [Models and metadata](#models-and-metadata)
- [Queries and relationships](#queries-and-relationships)
- [Events and validation](#events-and-validation)
- [Templates](#templates)
- [Scheduled work](#scheduled-work)
- [Storage](#storage)
- [Further reading](#further-reading)

## Get started

The main module requires Go 1.24 or newer. Create a module for the example:

```sh
mkdir shop && cd shop
go mod init example.com/shop
go get github.com/metadata-driven/mdd
mkdir models
```

The example uses in-memory storage, so its data lasts only as long as the application is open. Create these files in the `shop` directory:

```text
shop/
├── go.mod
├── main.go                 # Application entry point
└── models/
    ├── meta.yaml           # Models, relationships, presentation, and storage
    ├── meta.go             # Embedded metadata
    ├── app.go              # Application
    ├── context.go          # Transaction context and model sets
    ├── user.go             # User model and its business logic
    ├── user_type.go        # User type enumeration
    └── order.go            # Order model
```

The files in `models/` use `package models`; `main.go` imports that package. The model wrappers shown below correspond to the metadata and can be written by hand or generated.

### Metadata

**`models/meta.yaml`** — declare the application model:

```yaml
project:
  name: shop

sources:
  - name: main
    conn: 'mem:'

models:
  - name: user
    source: main
    set: users
    title: '@{login} ({name})'
    icon: '{type.icon}'
    attrs:
      - name: login
        type: string
      - name: name
        type: string
      - name: type
        type: enum
        items:
          - name: admin
            title: Administrator
            icon: '⭐'
          - name: customer
            title: Customer
            icon: '👤'
      - name: orders
        type: set<order>
        query: 'where(user={id}) order(amount desc)'

  - name: order
    source: main
    set: orders
    attrs:
      - name: user
        type: user
      - name: amount
        type: number(12,2)
```

The `user` model has a `type` enum and a computed `orders` set. Each order stores a reference to its user. The query on `orders` is evaluated for the particular user; `{id}` is replaced with that user's ID.

### Embed the metadata

**`models/meta.go`** — load the YAML once, before model registration:

```go
package models

import (
	_ "embed"

	"github.com/metadata-driven/mdd/meta"
)

//go:embed meta.yaml
var metaYAML []byte

var Meta = meta.MustParseYAML(metaYAML)
```

### Define the application and context

**`models/app.go`** — bind the application to its context type:

```go
package models

import "github.com/metadata-driven/mdd"

type App struct{ mdd.Application[Context] }
```

**`models/context.go`** — expose typed sets and access to the application:

```go
package models

import "github.com/metadata-driven/mdd"

type Context struct{ mdd.Context }

func (ctx *Context) App() *App               { return mdd.App[App](ctx) }
func (ctx *Context) Users() *mdd.Set[User]   { return mdd.NewSet[User](ctx) }
func (ctx *Context) Orders() *mdd.Set[Order] { return mdd.NewSet[Order](ctx) }
```

### Define the models and enum

These Go declarations are the model API corresponding to the metadata. Register each model before opening the application. The framework uses the registration to create the correct Go type when reading a set or a reference.

**`models/user.go`** — accessors, the computed `orders` set, and a validation hook:

```go
package models

import "github.com/metadata-driven/mdd"

type User struct{ mdd.Object }

var _ = mdd.RegisterModel[User](Meta, "user")

func (u *User) Context() *Context       { return mdd.Ctx[Context](u) }
func (u *User) ID() uint64              { return mdd.ID(u) }
func (u *User) Login() string           { return mdd.Attr[string](u, "login") }
func (u *User) Name() string            { return mdd.Attr[string](u, "name") }
func (u *User) Type() *UserType         { return mdd.Attr[*UserType](u, "type") }
func (u *User) Orders() *mdd.Set[Order] { return mdd.AttrSet[Order](u, "orders") }

func (u *User) SetLogin(v string) *User   { return mdd.SetAttr(u, "login", v) }
func (u *User) SetName(v string) *User    { return mdd.SetAttr(u, "name", v) }
func (u *User) SetType(v *UserType) *User { return mdd.SetAttr(u, "type", v) }

// OnCommit runs before a changed user is committed.
func (u *User) OnCommit() {
	if u.Login() == "" {
		panic("login is required")
	}
}
```

**`models/user_type.go`** — expose the predefined enum values as Go pointers:

```go
package models

import "github.com/metadata-driven/mdd"

type UserType struct{ mdd.EnumItem }

var (
	UserTypeAdmin    = mdd.Item[UserType](Meta, "user.type.admin")
	UserTypeCustomer = mdd.Item[UserType](Meta, "user.type.customer")
)
```

`mdd.Item` returns shared predefined enum objects and registers the enum type when needed. Their metadata remains available through methods such as `Title()` and `Icon()`.

**`models/order.go`** — an order refers to a user:

```go
package models

import "github.com/metadata-driven/mdd"

type Order struct{ mdd.Object }

var _ = mdd.RegisterModel[Order](Meta, "order")

func (o *Order) User() *User                { return mdd.Attr[*User](o, "user") }
func (o *Order) Amount() float64            { return mdd.Attr[float64](o, "amount") }
func (o *Order) SetUser(v *User) *Order     { return mdd.SetAttr(o, "user", v) }
func (o *Order) SetAmount(v float64) *Order { return mdd.SetAttr(o, "amount", v) }
```

### Open the application and use the models

**`main.go`** — create a user and an order inside a transaction:

```go
package main

import (
	"fmt"
	"log"

	"example.com/shop/models"

	"github.com/metadata-driven/mdd"
)

func main() {
	app, err := mdd.NewApp[models.App](models.Meta)
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()

	err = app.Execute(nil, func(ctx *models.Context) {
		alice := ctx.Users().Add().
			SetLogin("alice").SetName("Alice").SetType(models.UserTypeAdmin)
		ctx.Orders().Add().SetUser(alice).SetAmount(42.50)

		fmt.Println(alice.String())                       // @alice (Alice)
		fmt.Println(alice.Icon())                         // ⭐
		fmt.Println(alice.Type().Title())                 // Administrator
		fmt.Println(alice.Type() == models.UserTypeAdmin) // true
		fmt.Println(alice.Orders().Count())               // 1
	})
	if err != nil {
		log.Fatal(err)
	}
}
```

Run the example from the `shop` directory:

```sh
go run .
```

Output:

```text
@alice (Alice)
⭐
Administrator
true
1
```

`Execute` creates a context and commits when the callback returns normally.
It turns panics into errors and rolls back any transactions still open. A nil
first argument uses `context.Background()`. See
[Transactions](docs/runtime.md#transactions) for the order of writes, commits,
and rollbacks.

## Models and metadata

Embed `mdd.Object` in model types and use `mdd.Attr[T]` and `mdd.SetAttr` to implement accessors. `mdd.Ctx[Context]` gives a model its typed application context, and `mdd.ID` provides a nil-safe ID helper for a model pointer. Setters return the model, allowing calls to be chained.

Enums embed `mdd.EnumItem`. Predefined items can be compared by pointer and provide metadata methods such as `Title()`, `Description()` and `Icon()`. Predefined model items are also available through `mdd.Item[T](Meta, "model.item")`.

Objects belong to their transaction context. For ordinary records, pass IDs between `Execute` calls and look up the object again in the new context. `Get(id)` creates a lazy reference; `Deleted()` checks whether the record exists. A missing or deleted object's stored attributes return zero values. `Delete()` removes the object without cascading to references.

For generic tools, `ctx.NewSet("users")` selects a set by metadata name and returns an `*mdd.ObjectSet` rather than a typed set.

See [`tests/meta.yaml`](tests/meta.yaml), [`tests/user.go`](tests/user.go), [`tests/order.go`](tests/order.go) and [`tests/enums.go`](tests/enums.go) for a larger application model, including collections, predefined objects and computed sets.

## Queries and relationships

Inside the `app.Execute` callback in **`main.go`**, sets provide creation, filtering, ordering and iteration:

```go
for user := range ctx.Users().Filter("type", models.UserTypeAdmin).Order("login").All() {
	fmt.Println(user.Login())
}

first := ctx.Users().Filter("login", "alice").First()
count := ctx.Orders().Where("amount", ">", 10).Count()
_ = first
_ = count
```

Use `Find()` as an alias for `First()`, or `MustFind()` to panic with `mdd.ErrNotFound` if no match exists. `Limit`, `Offset`, `Fetch`, `Exists` and `Delete` operate on sets as well. `Order("-amount")` selects descending order.

In the same **`main.go` callback**, xquery can also be applied to a set or used to create one:

```go
orders := ctx.Orders().Query("where(amount > 10) order(amount desc) limit(5)")
allOrders := mdd.NewQuery[models.Order](ctx, "from(orders) order(amount)")
_ = orders
_ = allOrders
```

`&=` requires every listed element and `|=` requires at least one. Both operators
are only valid on collection fields. The [`tests` model](tests/meta.yaml) has a
`permissions` collection:

```go
users := ctx.Users().Query("where(permissions &= manage_users, manage_orders)")
users = ctx.Users().Query("where(permissions |= manage_users, manage_orders)")
users = ctx.Users().WhereContainsAny("permissions", models.UserPermissionManageUsers, models.UserPermissionManageOrders)
```

The Go API also provides `WhereContainsAll`. See the
[runtime reference](docs/runtime.md) for collection query semantics and driver
support.

A `set<order>` attribute with a `query` is accessed with `mdd.AttrSet[Order]`, as shown by `User.Orders()` above. The query's placeholders are evaluated against that user. Query template placeholders passed to `Set.Query` are evaluated against the application context. See [`tests/query_test.go`](tests/query_test.go) and the [runtime reference](docs/runtime.md) for examples and supported behavior.

## Events and validation

Hooks are optional methods on the application context or on models. The `User.OnCommit` example validates the login by panicking; `Execute` catches the panic and returns an error. An ordinary Go error can likewise be passed to `panic(err)`.

- Context `OnInit()` runs before the `Execute` callback; context `OnCommit()` runs when committing.
- Model `OnNew()` runs when a new object receives its ID. `OnDelete()` runs when an existing object is deleted.
- Model `OnCommit()` runs before commit for new or changed objects. It can change objects or create others; their changes are included in the commit.
- Model `OnAfterCommit()` runs after a successful commit. It can read data but cannot modify it through the same context. An error here is returned after the original data has already committed; it cannot undo that commit.

For example, add this method to **`models/context.go`**:

```go
func (ctx *Context) OnInit() {
	ctx.Log("transaction started")
}
```

Add the following methods to **`models/user.go`** alongside `OnCommit`:

```go
func (u *User) OnNew() {
	u.Context().Logf("new user ID: %d", u.ID())
}

func (u *User) OnDelete() {
	u.Context().Logf("deleted user ID: %d", u.ID())
}

func (u *User) OnAfterCommit() {
	u.Context().Logf("committed user ID: %d", u.ID())
}
```

Model `datetime` attributes named `created` and `updated` are filled automatically: an empty `created` before the initial write, and `updated` for new or changed objects before `OnCommit`. See the [lifecycle reference](docs/runtime.md) and [`tests/events_test.go`](tests/events_test.go) for details and examples.

## Templates

The Go runtime uses [xtpl](https://github.com/goldic/xtpl) by default. This is a runtime choice, not a requirement of the metadata specification. Model `title`, `description` and `icon` expressions are evaluated against a particular model instance. The following fields from **`models/meta.yaml`** are templates:

```yaml
title: '@{login} ({name})'
icon: '{type.icon}'
```

For a user with login `alice`, name `Alice` and the `admin` enum value from the example, `user.String()` returns `@alice (Alice)` and `user.Icon()` returns `⭐`. Placeholders can traverse model attributes or resolve Go methods; [`tests/user.go`](tests/user.go) and [`tests/meta.yaml`](tests/meta.yaml) show a computed method used in a template.

`mdd.TemplateEngine.Text` and `mdd.TemplateEngine.Query` can be set to custom parsers before opening the application. The runtime compiles templates when the application opens and caches them for reuse. See [`template.go`](template.go) and the [runtime reference](docs/runtime.md) for the parser interface.

## Scheduled work

`Cron` blocks until its context is canceled. In **`main.go`**, after opening the application, start it in a goroutine and stop it before closing the application. This snippet requires the standard `context` and `time` imports:

```go
cronCtx, cancel := context.WithCancel(context.Background())
done := make(chan struct{})
go func() {
	defer close(done)
	app.Cron(cronCtx, time.Minute, func(ctx *models.Context) {
		ctx.SetTimeout(15 * time.Second)
		ctx.Log("processing orders")
		// Process orders in this transaction.
	})
}()

// At shutdown:
cancel()
<-done
```

Each run uses a new context and transaction, with the log prefix `[cron]`. Runs start at boundaries measured from the Unix epoch, never overlap, and skip boundaries missed during a run. A failed run is logged and later runs continue. Cancellation while waiting is checked at least every 15 seconds; a running callback must cooperate with cancellation. `SetTimeout` must be called before opening a driver transaction.

## Storage

The main module registers the `mem`, `files`, `json` and `yaml` storage drivers. MySQL support is available in `github.com/metadata-driven/mdd/driver/mysql` and must be imported for registration. SQLite is a [separate Go module](driver/sqlite/go.mod), using the pure-Go `modernc.org/sqlite` driver; install and import `github.com/metadata-driven/mdd/driver/sqlite` when needed. Check its `go.mod` for its Go version requirement.

For example, add one of these blank imports to **`main.go`** to register the database driver your application uses:

```go
import _ "github.com/metadata-driven/mdd/driver/mysql"

// Or, after adding the separate SQLite module:
import _ "github.com/metadata-driven/mdd/driver/sqlite"
```

Choose a source using its connection string in metadata. Driver capabilities and database restrictions can differ, including support for indexes and full-text queries. See the [runtime reference](docs/runtime.md) and the driver packages for configuration and details.

## Further reading

- [MDD metadata specification](https://github.com/metadata-driven/spec/blob/main/specification.md) and its [canonical example](https://github.com/metadata-driven/spec/blob/main/examples/canonical.yaml)
- [Go runtime reference](docs/runtime.md) and [local metadata format reference](docs/metadata-format.md)
- [Example application and integration tests](tests/)
- [Go package documentation](https://pkg.go.dev/github.com/metadata-driven/mdd)
