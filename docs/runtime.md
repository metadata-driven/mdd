# Runtime and example

The root `mdd` package provides `Application`, `Context`, `Object` and the
`Set[T]` query. `Open` converts metadata to driver schemas, opens the sources,
and initializes configured items. Import a driver package to register its
connection scheme; the memory driver is registered by default.

Applications that use SQLite can import `github.com/metadata-driven/mdd/driver/sqlite`
for its registration side effect. The SQLite module requires Go 1.26.0. The
root module requires Go 1.24.0.

From the repository root, run the root and SQLite tests separately.

```sh
GOWORK=off go test ./...
(cd driver/sqlite && GOWORK=off go test ./...)
```

The root suite includes MySQL tests that expect a local `testdb` database.

An application owns its connections. Use `app.Execute` to run work in a typed
context; the context is created and closed by the application. Objects and sets
are bound to that context and must not be used after it closes. A
context and its objects are not intended for concurrent use. Sets are mutable: `Where`, `Filter`, `Order`, `Limit`, and `Offset` update the
receiver and return it for chaining. `Where(name, op, values...)` adds a
condition; `Filter(name, values...)` adds an equality condition. Multiple
conditions are combined with AND. For equality, multiple values are
alternatives; for inequality, the value must differ from all supplied values.
Model and enum arguments are converted to IDs. Prefix a column name with `-`
for descending order. Use a new
set for an independent query. `First`, `Find`, `MustFind`, `FindBy`, and
`FindOrAdd` do not change the set's query. `Add` creates a record. `All` returns
an `iter.Seq[*T]` that captures the query when `All` is called and executes it
when iteration starts. Consume the iterator before
closing the context. Use `slices.Collect(set.All())` when a slice is needed.
Drivers use `driver.Query` independently of the [`xquery`](../xquery) package. `Table` identifies
the table and `Index` optionally selects one of its indexes. `Filters` and
`Order` specify conditions and per-field ordering; `Limit` and `Offset`
control pagination. When no explicit order is supplied, results follow the
selected index or the primary ID order. Equal sort values are ordered by ID,
using the direction of the last sort column. `Offset` is an opaque continuation
value returned by `Fetch` for the last delivered record, not a row count.
`Extra` is reserved for driver-specific queries; the current drivers reject it.
Driver `Fetch` returns record IDs.

`Set.Query` adds conditions and ordering from an xquery expression. The set's
model is the default `from`, and `{...}` expressions are evaluated against the
application context. Existing conditions and ordering remain first; specified
nonzero limits and nonempty offsets replace the previous values. Use `Limit(0)`
or `Offset("")` to clear them. Queries without `{...}` expressions are cached
per application schema and source; each set receives its own query values.

```go
orders := ctx.Orders().Query("where(status=placed) order(ts desc)")
// Or construct a set directly:
orders = mdd.NewQuery[Order](ctx, "where(status=placed) order(ts desc)")
```

`mdd.AttrSet[T](object, name)` builds a set from the attribute's metadata query.
It evaluates `{...}` expressions against the object using its Go methods and
stored attributes, parses the result with `xquery`, and translates its
filters, ordering, limit and offset into a driver query. Each call creates an
independent set. The current translation supports model sets and stored fields
in the model's primary table; `select`, nested paths and computed query fields
are not supported and cause a panic. The chosen driver can impose additional
query restrictions.

Metadata stores presentation and query expressions as strings without parsing
them. `Application.Open` compiles and caches the templates, reporting syntax
errors with the metadata path and field. Applications can replace
`mdd.TemplateEngine.Text` and `mdd.TemplateEngine.Query` before opening. The
default query template parser uses JSON escaping. The application captures the
configured parsers when it opens; later `Set.Query` expressions are compiled
and cached on first use. Rendered set queries are parsed when used, with the set
as their default source.

Expressions are resolved according to the configured template engine. They can
use computed values that have no corresponding metadata attribute. Query field
names outside template expressions continue to use metadata names.

```go
func (u *User) Orders() *mdd.Set[Order] {
    return mdd.AttrSet[Order](u, "orders")
}
```

The driver value contract uses `uint64` for IDs, `int64` for integers, `bool`,
`string`, `time.Time` in UTC with microsecond precision, `[]byte` for binary,
`driver.File` for files, and
decoded JSON values. Datetimes are truncated to microseconds before storage.
Decimal
values use base-10 strings, preserving precision; converting a floating-point
input cannot recover precision already lost by that input. JSON numbers use
`json.Number`. Collections are JSON arrays at the driver boundary. References
inside them are stored as IDs and resolved by `Attr`. Index support for
collections depends on the driver. Absent JSON, binary and file values are returned as nil.

Model and set operations return values directly and panic on failures. `Find`,
`First`, `Last`, `FindBy` and `FindByID` return nil when no record matches;
`MustFind` panics with `mdd.ErrNotFound`. `Get` returns a lazy object for any
nonzero ID without checking the record; `Deleted` checks whether it exists.
Transaction operations panic on failure. `app.Execute` catches the panic, rolls
back open transactions, and returns the failure as an error to its caller.
Driver transactions and context lifecycle are internal to the application.

Application-specific contexts embed `mdd.Context` by value. The
application embeds `mdd.Application[Context]` and is bound when opened:

```go
type App struct { mdd.Application[Context] }
type Context struct { mdd.Context }

func (ctx *Context) App() *App { return mdd.App[App](ctx) }

//go:embed meta.yaml
var metaYAML []byte

var Meta = meta.MustParseYAML(metaYAML)

func NewApp() (*App, error) {
    return mdd.NewApp[App](Meta)
}

err := app.Execute(nil, func(ctx *Context) {
    // Use ctx here.
})
```

An optional `context.Context` can be passed as the first argument to control
cancellation. The context is committed on success and rolled back on panic.

Call `ctx.SetTimeout(15 * time.Second)` before the first database transaction to
set a timeout for one `Execute` call, including a single `Cron` run. Calling it
again before a transaction opens replaces the timeout; the parent context's
earlier deadline still applies. Setting a timeout after opening a transaction
panics. MDD checks cancellation during database operations and before commit;
application code must cooperate with cancellation during its own work.

Applications use Go's standard logger by default. Configure the logger before
creating contexts, or override it for an individual context:

```go
app.SetLogger(logger, "app: ")
err := app.Execute(nil, func(ctx *Context) {
    ctx.SetLogPrefix("request: ")
    ctx.Log("processing request")
    ctx.Logf("user ID: %d", userID)
})
```

`Logger()` exposes the configured `*log.Logger`. A nil logger passed to the
application selects the standard logger; a nil context logger selects the
application logger. Prefixes are added without modifying the supplied logger.
Each `Execute` context inherits the application logger. `ctx.SetLogPrefix`
sets or replaces the context prefix, and `ctx.SetLogger` can replace its logger.
An empty prefix removes the context prefix. Errors returned
by `Open`, `Close`, and `Execute` are also written to the appropriate logger.

`ctx.App()` returns the original `*App`. The underlying transaction and
application links are internal to `mdd`.

`app.Cron` runs a callback in a new transaction at each period boundary:

```go
cronCtx, stopCron := context.WithCancel(context.Background())
done := make(chan struct{})
go func() {
    defer close(done)
    app.Cron(cronCtx, time.Minute, func(ctx *Context) {
        ctx.SetLogPrefix("cron: ")
        ctx.ProcessOrders()
    })
}()

// Stop the cron and wait for an active callback before closing the app.
stopCron()
<-done
```

`Cron` blocks until its context is canceled. The first run waits for the next
boundary, measured from the Unix epoch. Runs do not overlap; boundaries missed
while a callback is running are skipped. A failed transaction is logged by
`Execute`, and the cron continues. A nonpositive period causes a panic. Canceling
the context prevents further runs; an active callback must handle cancellation
itself. Stop and wait for cron goroutines before closing the application.
While waiting for a boundary, cancellation is checked at least every 15 seconds.

Models can likewise embed `mdd.Object` by value. Register each Go
model type and its metadata name at package initialization, then pass the
application context to `NewSet`:

```go
type User struct { mdd.Object }

var _ = mdd.RegisterModel[User](Meta, "user")

func (ctx *Context) Users() *mdd.Set[User] {
    return mdd.NewSet[User](ctx)
}

func (u *User) Context() *Context { return mdd.Ctx[Context](u) }
```

For tools that select models at runtime, `ctx.NewSet("user")` (or
`ctx.NewSet("users")`) returns an `*mdd.ObjectSet`. It supports the same queries
and operations as a typed set and returns `*mdd.Object`. `set.Model()` returns
the model metadata. If a Go model type is registered, its hooks and templates
still run on that model; the dynamic set exposes its embedded object.

`user.Context()` returns the original `*Context`. The public methods on
`*mdd.Object` handle nil receivers. Go dereferences embedded fields
before calling promoted methods, however, so calling `user.ID()` or
`user.Context()` on a nil `*User` requires an explicit wrapper. The example
models define a nil-safe `ID` method. Attribute getters implemented with
`mdd.Attr` can be chained through nil model pointers.

A nil context produces a nil set, and nil set read operations return zero values
or an empty iterator. The non-generic object state is embedded in the model;
its context stores pointers to that state without an additional object allocation.
Models must be used through their original pointers rather than copied by value.

A transaction context retains one model object per model type and record ID.
`Add`, `Get`, query results, and references return that same object during the
transaction, so pointer comparison is meaningful within it. Objects obtained
in different transactions are distinct. The context releases its cache when it
commits or rolls back; retained objects remain bound to the closed context.

A nil `*mdd.Object` returns zero from `ID`. Binary attributes
are accessed as byte slices; file
attributes return a lazy `mdd.File`:

```go
func (u *User) Token() []byte {
    return mdd.Attr[[]byte](u, "token")
}

func (u *User) Avatar() mdd.File {
    return mdd.Attr[mdd.File](u, "avatar")
}

func (u *User) SetAvatar(value any) *User {
    return mdd.SetAttr(u, "avatar", value)
}
```

Read a non-nil file with `Read`, `io.ReadAll`, or `Bytes`. Close an opened file
before the transaction ends. The file is tied to its transaction; its source
may be opened again after reading or closing if it supports reopening.

Model registration associates a Go type with a metadata model for sets and
reference resolution. Register model types before opening the application.
`Open` validates each registered model and snapshots its type in the storage
schema. Models embed `mdd.Object`; it does not have to be the first field.
The factory creates models through reflection, with additional fields set to
their zero values. Attribute conversion in `Attr` can also use reflection.

An unregistered model uses `*mdd.Object` for references. Read such a reference
with `mdd.Attr[*mdd.Object]`; its target model is identified by the attribute's
metadata. `NewSet[T]` still requires a registered model type.

`mdd.SetAttr` updates an attribute and returns the same model, so typed model
setters can be chained. Each object keeps its pending changes, while the context
tracks the one object awaiting a flush, including attributes stored in different
sources. It flushes those changes when changing another object, reading an
uncached and unbuffered attribute, requesting an ID for a new object, executing
a query, or committing. Cached reads do not flush. Reading an attribute of a new
object without an ID returns nil, even if the attribute has been set. Reading a
buffered file attribute of a saved object flushes it before returning a reader.
Rollback discards pending changes. Before a flush, non-file changes are merged
into the object's cache; only the changed fields are sent to the driver. File
changes are read back from the driver. The cache retains the values sent to the
driver until commit or rollback, including when the database transforms them.
Errors from a buffered write panic when it is flushed. File attributes accept
an `io.Reader`, `[]byte`, or string; binary attributes accept `[]byte` or string.
A nil model returns nil without writing.

`Object.Delete()` removes the object's records from its storage sources without
changing references in other objects. A reference to a deleted or missing record
keeps its ID; `Deleted()` reports true, `Get()` returns nil for stored attributes,
and `Attr[T]` returns the zero value. Setting a known deleted object panics with
`ErrNotFound`. `Deleted()` checks the primary source when the object's existence
is not already known in the current transaction. Its cached state is cleared on
commit and rollback. At the driver level, `Get` returns `driver.ErrNotFound` for
a missing record, while deleting one is a no-op.

`Object.UID()` returns the model path and object ID in the form `user#123`.
For a new object, it writes the object to obtain its ID, just like `ID()`.
It returns an empty string when the object has no ID, including a nil object or
an object deleted before it was written.

If a model declares a scalar `datetime` attribute named `created`, a new
object's empty value is filled with the current time before its first write and
`OnNew`. An explicitly supplied time is preserved. A scalar `datetime`
attribute named `updated` is set to the current time before `OnCommit` for each
new or changed object, including models without event handlers. Other types and
collections named `created` or `updated` are not filled automatically. Both
timestamps participate in the transaction and are discarded on rollback.

Application context types can optionally define `OnInit()` and `OnCommit()`.
`OnInit` runs once after the context is initialized and before the `Execute`
callback. `OnCommit` runs once at the start of each commit, including an
explicit `Commit`, before pending changes are flushed and model `OnCommit`
hooks run. It can change or create objects; their model hooks run normally.
Calling `Commit` or `Rollback` recursively from `OnCommit` is not supported.
A panic in either hook is handled like a panic in the `Execute` callback.

Model types can optionally define `OnNew()`, `OnDelete()`, `OnCommit()`, and
`OnAfterCommit()` methods. `OnNew` runs when a new object receives its ID, after
its initial values are written. `OnDelete` runs immediately after an existing
object is deleted within the transaction. Its ID remains available, `Deleted()`
returns true, and its stored attributes return their zero values. It does not
run for missing objects or new objects that have not been written. Recursive
deletion of the same object during its `OnDelete` is ignored. `Set.Delete()`
invokes `OnDelete` for each object.

`OnCommit` runs once per commit for each new or changed object before the
underlying transactions are committed. Modified and deleted objects are tracked
in one queue, processed in order of their first change. Changes and new objects
created by the hooks are flushed, and newly changed objects receive their own
`OnCommit`. Objects deleted before their turn do not receive `OnCommit`.
Deleting an object runs its `OnDelete` immediately, even if its `OnCommit`
was already processed. A hook failure aborts the transaction; calling `Commit`
or `Rollback` recursively during commit is not supported.

`OnAfterCommit` runs once for each written or deleted object after all involved
transactions commit successfully. It can read cached attributes and execute
queries. Uncached attributes are read through new transactions, opened lazily
and rolled back after all callbacks. Cached file values are discarded before
the callbacks and read again because they belong to the completed transactions.
The remaining cache is cleared after the callbacks. Cached values reflect what
the original transaction read or wrote; later reads may observe newer database
state, but do not overwrite other cached attributes. The context cannot change
model data, commit, or roll back during the callbacks. A read transaction stays
open until all callbacks finish; drivers
that serialize transactions can block a nested `Execute` on the same source.
Panics are collected while remaining callbacks continue;
`Execute` returns an error identifying `OnAfterCommit` and the affected objects.
Wrapped errors can be inspected with `errors.Is` and `errors.As`. The transaction
has already committed and cannot be rolled back. When `Commit` is called
explicitly, it panics with that error. Callbacks are not guaranteed to run if
the process stops after commit. Different database connections do not share an
atomic commit; a partial commit failure does not run these callbacks.

`Set.Delete()` selects IDs using the current filters, ordering, limit and offset,
then deletes the selected objects individually. References are not cascaded.

Unique indexes enforce uniqueness of the stored values, including defaults
for attributes that were not explicitly set. MySQL can index bounded binary
columns stored as VARBINARY, subject to its index limits. It cannot create a full
unique index over unbounded or large binary or file columns. MySQL stores
strings with a length of 1 to 1023 in VARCHAR; unbounded strings and strings
of at least 1024 characters use LONGTEXT and cannot be indexed by this driver.
The `mem`, `file`, JSON and YAML drivers reject indexes over file and JSON
columns (including collections) and check uniqueness by scanning records.

```go
func (o *Order) SetPaid(value bool) *Order { return mdd.SetAttr(o, "paid", value) }
order.SetPaid(true).SetAmount(42.50)
```

Enumerations are immutable objects backed by metadata, not database records.
Declare a wrapper and its shared values using the same `Item` utility used for
predefined model objects:

```go
type UserType struct { mdd.EnumItem }

var UserTypeAdmin = mdd.Item[UserType](Meta, "user.type.admin")
```

`Item` registers the wrapper type for its enumeration. `RegisterEnum` is also
available when no predefined variable is needed. Reading an enum attribute
returns the same pointer from every context using that metadata, so values can
be compared with `==`. Enum items provide `ID`, `Name`, `Title`, `Description`,
`Icon`, and `Get`. Enum IDs in persistent configurations should be explicit
and stable.

Registrations belong to the specific `meta.Config`. They must be completed
before opening the application; applications opened from different configs do
not share Go type registrations. Registration panics for invalid paths,
invalid pointer types, or conflicting registrations. Application opening
reports an error if a model's `MDDObject()` does not return a non-nil
`*mdd.Object`.

Predefined model items can be declared as shared references:

```go
var UserSuperAdmin = mdd.Item[User](Meta, "user.super_admin")
```

All contexts using the same metadata return the same pointer for that model
and ID. A predefined model object has no transaction context, reads its constant
attribute values from metadata, and rejects updates and deletion. On opening,
the application writes those records to storage, overwriting records with the
same IDs and resetting unspecified fields to their storage defaults. This makes
the records available to queries and reserves their IDs.

`Set.FindBy` returns nil when no record matches. `First` and `Last` use the
query's ordering, limit and offset. `FindOrAdd` returns the first match or
creates a record and initializes it from equality filters, using the first value
for each filter. Other operators do not assign values, and IDs are assigned by
the driver. `FindOrAdd` is a lookup followed by an insert, not an atomic
uniqueness guarantee across transactions.

`Object.Set` accepts a model reference as an integer ID, a model instance,
zero, or nil. A model instance must have the referenced metadata type and
belong to the same context; numeric IDs are stored without checking whether the
record exists. Enum values accept registered enum objects and integer types,
including named Go types. Zero means unset; other IDs must exist in the configured
enum. Enum objects must have the expected metadata type and belong to the same
application. The same rules apply to reference and enum collections. Named Go
integer types are treated as IDs and validated against the attribute.

A model method can then resolve a reference:

```go
func (o *Order) User() *User {
    return mdd.Attr[*User](o, "user")
}
```

`Attr` uses metadata to read scalar values, model references and collections.
For example, `mdd.Attr[[]string](user, "tags")` reads strings and
`mdd.Attr[[]*User](user, "friends")` resolves model references. The
requested Go model type must be registered for the model named by the
attribute. A null or zero reference returns nil. A nonzero reference returns a
lazy object, including when the record is missing; `Deleted` checks its primary
source. Attributes load their own source on first access and the object caches
the row. `Attr` returns its zero
value when passed a nil model. To make a model accessor nil-safe, pass the
model itself to the helper, not its embedded `Object` field:

```go
func (u *User) Login() string {
    return mdd.Attr[string](u, "login")
}
```

With nil-safe accessors, `order.User().Login()` returns an empty string when
there is no user. A nil context and nil set also allow chained reads such as
`order.User().Context().Users().First().Login()`. Methods on application model
wrappers must handle nil receivers explicitly where Go's promoted methods would
dereference an embedded value. A nil model
has no metadata, so the helper cannot validate attribute names or target types
in that case. Model methods that access fields or promoted methods directly
must handle nil themselves. Operations on a nil set return zero values; they do
not create records. `MustFind` still panics with `ErrNotFound`. Errors while
reading an existing model remain visible.

`tests` shows typed model wrappers and relationships using its embedded
configuration. Run the example checks with:

```sh
go test ./tests
```

The example uses memory storage, so its data lasts only for the life of the
application. Its configuration is a runnable subset of the larger metadata
example in `meta/testdata/config.yaml`.

A context opens each source lazily and keeps its own transaction for that
source. Commits across sources are sequential and are not atomic: an error can
leave earlier sources committed. Predefined items are initialized per source;
the file driver is excluded because its commits are not atomic. Driver `Set` creates a record with the specified ID when
needed and updates only
the supplied fields, including for secondary sources.

`Set.Page` returns the driver's continuation offset. MySQL's driver supports
continuation only when ordering by ID; the runtime rejects other MySQL ordering
with an offset. File operations use a driver's streaming interface when
available and otherwise buffer the value in memory. Binary collections are
stored as JSON arrays of base64 strings and read as `[][]byte`. Boolean, JSON,
and file collections are not supported. SQLite and MySQL expose JSON numbers as `json.Number` when
reading raw values to preserve their precision.

An existing item ID is treated as already initialized; its values and identity
are not checked against the configuration. Reserve configured IDs for those
items when using persistent storage.

`Object.Get` reads the driver row, including binary byte slices. Files are
returned as lazy readers.
