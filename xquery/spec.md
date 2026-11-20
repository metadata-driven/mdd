# Query Language Specification v1

## General form

A query consists of directives:

```text
directive(arguments)
```

Example:

```text
# Select orders

from(billing.orders)

select(
    icon,
    ts,
    status,
    user.name,
    amount,
)

where(status = done, canceled)
where(user.type = guest, moderator)
where(amount > 1)

order(
    user.type asc,
    ts desc,
)

limit(100)
offset(200)
```

Single-line form:

```text
from(billing.orders) select(icon,ts,status,user.name,amount) where(status=done,canceled) where(amount>1) order(user.type asc,ts desc) limit(100) offset(200)
```

Directives can appear in any order.

---

# Comments

Both comment styles are supported:

```text
# comment

// comment
```

A comment extends to the end of the line.

Examples:

```text
from(billing.orders) // source

where(status = done) # completed orders only
```

Comments are allowed inside multiline directives:

```text
select(
    id,
    status, // order status
    amount,
)
```

Comment markers inside strings are treated as literal text:

```text
where(url = "https://example.com/a//b")
where(tag = "#important")
```

---

# Supported directives

```text
from(...)
select(...)
where(...)
order(...)
limit(...)
offset(...)
```

Occurrence limits:

```text
from      at most once
select    any number of times
where     any number of times
order     any number of times
limit     at most once
offset    at most once
```

All `where(...)` conditions are combined with `AND`.

A source is required. Specify it either with a `from(...)` directive or with
the `"from"` argument when calling the parser.

---

# from

Specify the source as an unquoted table name. It must contain at least one
character and can use ASCII letters, digits, underscores, hyphens, and dots.
Dots are part of the name; the parser does not split it into components.
Slashes are not allowed in table names. The same rules apply to the parser's
`"from"` argument.

```text
from(users)

from(billing.orders)

from(2026-orders_archive)
```

---

# select

Separate field names with commas:

```text
select(
    id,
    status,
    amount
)
```

Nested fields:

```text
user.name

user.profile.avatar

parent.icon
```

A dot always separates field path components. A field name cannot contain a
literal dot. This rule also applies to field paths in `where(...)` and
`order(...)`.

---

# Literals

JSON literals are supported:

```text
123

-45

12.5

true

false

null

"hello"

[]

[1, 2, 3]

["a", "b"]

[true, false]
```

An array is an alternative way to write a condition's value list. Nested
arrays are allowed and are recursively flattened into a single list:

```text
where(matrix = [[1, 2], [3, [4, 5]]])
```

After parsing:

```go
Values: []any{int64(1), int64(2), int64(3), int64(4), int64(5)}
```

Each element of the resulting `Condition.Values` is a primitive value.

Unquoted string literals are also supported:

```text
done

canceled

guest

moderator

GB
```

An unquoted string is equivalent to its quoted form:

```text
where(status = done, canceled)
```

is equivalent to:

```text
where(status = "done", "canceled")
```

which is equivalent to:

```text
where(status = ["done", "canceled"])
```

The parser always treats these unquoted literals as strings.

A string may later be converted to a numeric ID based on field metadata.

For example:

```text
"done"     → 111

"canceled" → 222
```

---

# where

General form:

```text
where(field operator values)
```

Examples:

```text
where(status = done)

where(status = done, canceled)

where(status = ["done", "canceled"])

where(amount > 1)

where(parent = null)

where(parent != null)
```

---

# Operators

Supported operators:

```text
=

!=

>

>=

<

<=

~

&=

|=

in

not in
```

Semantics:

```text
in      == =

not in  == !=
```

After parsing:

```text
in      → =

not in  → !=
```

---

# Collection membership

`&=` checks whether a collection contains all listed values. `|=` checks
whether it contains at least one of them:

```text
where(permissions &= manage_users, manage_orders)
where(permissions |= manage_users, manage_orders)
where(permissions |= [manage_users, manage_orders])
```

Both operators require at least one value and are stored unchanged in
`Condition.Op`. With a single value, both check whether that value is in the
collection. An empty collection never matches, and duplicate candidates do not
affect the result. The operators compare whole elements, not substrings.

Both operators are valid **only on collection fields**. The parser rejects an
empty value list, and field type validation rejects a scalar field. To match a
scalar field against any value in a list, use `=`:
`where(type = admin, moderator)`.

---

# Full-text search

Use the `~` operator for full-text search:

```text
where(description ~ "canceled by client request")
```

The operator takes exactly one value. The parser stores it unchanged in
`Condition.Op`:

```go
Condition{
    Field:  Field("description"),
    Op:     "~",
    Values: []any{"canceled by client request"},
}
```

Multiple values and arrays are not allowed:

```text
where(description ~ "one", "two")
where(description ~ ["one", "two"])
```

---

# Equality against a list

Single value:

```text
where(id = 1)
```

List of values:

```text
where(id = 1, 2, 3)
```

Equivalent form:

```text
where(id = [1, 2, 3])
```

Semantics:

```text
id ∈ {1,2,3}
```

---

# Inequality against a list

```text
where(id != 1)
```

```text
where(id != 1, 2, 3)
```

```text
where(id != [1, 2, 3])
```

Semantics:

```text
id ∉ {1,2,3}
```

---

# Empty value lists

```text
where(id = [])
```

This condition is always false. The parser stores it in `Query.Where` with an
empty `Values` list.

`Query.IsEmpty()` returns `true` if it finds a condition with the `=` operator
and an empty value list.

---

```text
where(id != [])
```

This condition is always true. The parser also stores it in `Query.Where`; it
does not optimize or remove conditions.

---

# Null

Test whether a value is absent:

```text
where(parent = null)
```

Test whether a value is present:

```text
where(parent != null)
```

---

# Comparison operators

Supported operators:

```text
>

>=

<

<=
```

These operators take exactly one value.

Valid:

```text
where(amount > 10)

where(ts >= 1000)
```

Invalid:

```text
where(amount > 1, 2)

where(amount > [1, 2])
```

---

# order

Example:

```text
order(
    user.type asc,
    ts desc
)
```

Sort directions:

```text
asc

desc
```

If you omit the sort direction:

```text
order(user.name)
```

is equivalent to:

```text
order(user.name asc)
```

---

# limit

```text
limit(100)
```

Only positive integers are allowed:

```text
limit(100)

limit(1000)
```

`Query.Limit` uses `0` to indicate that the directive is absent, so `limit(0)`
is an error.

---

# offset

```text
offset(200)
```

Offsets can be strings or non-negative integers up to `MaxUint64`:

```text
offset(0)

offset(200)

offset("next-page")
```

A numeric `Query.Offset` is stored as an `int64` if it fits, or as a `uint64`
otherwise. The supported range at query execution time depends on the driver.

---

# Trailing comma

Trailing commas are allowed:

```text
select(
    id,
    status,
    amount,
)
```

```text
where(
    status = done,
    canceled,
)
```

```text
order(
    ts desc,
    user.name asc,
)
```

---

# Go API

The parser parses and validates the query. It does not generate SQL.

Entry point:

```go
type Args map[string]string

func Parse(src string, args ...Args) (*Query, error)
```

You can omit `from(...)` from the query text if you pass the source as a parser
argument:

```go
query, err := Parse(`select(id, user.name)`, Args{"from": "billing.orders"})
```

An explicit `from(...)` directive takes precedence over the `"from"` argument.
If neither specifies a source, the parser returns an error.

Result types:

```go
type Query struct {
    From   string      `json:"from"`
    Select []Field     `json:"select,omitempty"`
    Where  []Condition `json:"where,omitempty"`
    Order  []Order     `json:"order,omitempty"`
    Limit  int64       `json:"limit,omitempty"`
    Offset any         `json:"offset,omitempty"`
}

type Field string

type Condition struct {
    Field  Field `json:"field"`
    Op     string `json:"op"`
    Values []any  `json:"values"`
}

type Order struct {
    Field Field `json:"field"`
    Desc  bool  `json:"desc"`
}
```

`From` stores the table name as written:

```go
query.From // "billing.orders"
```

A field path is stored as a single string, with `.`-separated components:

```go
field := Field("user.profile.avatar")
field.String() // "user.profile.avatar"
field.Parts()  // []string{"user", "profile", "avatar"}
```

`Field` methods:

```go
func (f Field) String() string
func (f Field) Parts() []string
```

`Condition.Values` has type `[]any`. After parsing, its elements have these types:

```text
integer in the int64 range              int64
integer above MaxInt64 up to MaxUint64  uint64
other numbers, including fractions      json.Number
string                                  string
true/false                              bool
null                                    nil
```

Numbers are preserved without rounding. `json.Number` retains the original
numeric literal. The field type determines how the value is converted later.

Nested arrays in query text are recursively flattened. The resulting
`Condition.Values` does not contain any nested `[]any` values.

Unquoted string literals such as `done` are stored as `string` values.

---

# Checking for an empty result

`Query` has no field that tracks whether the result is empty. Use this method
instead:

```go
func (q *Query) IsEmpty() bool
```

It scans `Where` and returns `true` if it finds a condition of this form:

```text
where(field = [])
```

The method returns `false` for a `nil` `*Query`.

---

# Formatting as xquery

`String()` returns the canonical single-line form:

```go
func (q *Query) String() string
```

Example:

```text
from(billing.orders) select(id, user.name) where(status = "done") order(ts desc) limit(10) offset("next-page")
```

Canonical formatting rules:

- Directives appear in this order: `from`, `select`, `where`, `order`, `limit`, `offset`.
- All string values are formatted as JSON strings.
- The default sort direction, `asc`, is omitted.
- An empty value list is formatted as `[]`.
- A `nil` `*Query` formats as an empty string.

Round-tripping preserves the query structure:

```text
Parse → Query.String → Parse
```

Comments and original formatting are not preserved.

---

# JSON

`Query`, `Condition`, and `Order` support standard JSON encoding and decoding
through `encoding/json`.

Example:

```json
{
  "from": "billing.orders",
  "select": ["id", "user.name"],
  "where": [
    {
      "field": "status",
      "op": "=",
      "values": ["done"]
    }
  ],
  "order": [
    {
      "field": "ts",
      "desc": true
    }
  ],
  "limit": 10,
  "offset": "next-page"
}
```

The output always includes `from`. The `omitempty` tag omits `select`, `where`,
`order`, `limit`, and `offset` when they are empty.

---

# Whitespace

These forms are equivalent:

```text
where(amount>1)
```

```text
where(amount > 1)
```

These forms are also equivalent:

```text
where(status=done,canceled)
```

```text
where(status = done, canceled)
```

---

# Normalization

The parser applies the following transformations.

Unquoted string literals:

```text
where(status = done, canceled)
```

↓

```go
Condition{
    Field:  Field("status"),
    Op:     "=",
    Values: []any{"done", "canceled"},
}
```

---

`in`:

```text
where(status in done, canceled)
```

↓

```text
where(status = done, canceled)
```

---

`not in`:

```text
where(status not in done, canceled)
```

↓

```text
where(status != done, canceled)
```

---

Array:

```text
where(id = [1, 2, 3])
```

↓

```go
Values: []any{int64(1), int64(2), int64(3)}
```

---

Empty value list:

```text
where(id = [])
```

↓

```go
Values: nil
```

---

# Grammar

```text
query =
    { comment | whitespace | directive }

directive =
      from-directive
    | select-directive
    | where-directive
    | order-directive
    | limit-directive
    | offset-directive

from-directive =
    "from" "(" table-name ")"

table-name =
    table-name-character { table-name-character }

table-name-character =
    letter | digit | "_" | "-" | "."

where-expression =
    field
    operator
    values

operator =
      "="
    | "!="
    | ">"
    | ">="
    | "<"
    | "<="
    | "~"
    | "&="
    | "|="
    | "in"
    | "not in"

values =
      value { "," value } [ "," ]
    | "[" [ value { "," value } [ "," ] ] "]"

value =
      json-string
    | json-number
    | json-boolean
    | json-null
    | json-array
    | identifier
```
