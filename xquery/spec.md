# Query Language Specification v1

## Общая форма

Запрос состоит из директив:

```text
directive(arguments)
```

Пример:

```text
# выборка заказов

from(billing/orders)

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

Однострочная запись:

```text
from(billing/orders) select(icon,ts,status,user.name,amount) where(status=done,canceled) where(amount>1) order(user.type asc,ts desc) limit(100) offset(200)
```

Порядок директив не имеет значения.

---

# Комментарии

Поддерживаются:

```text
# комментарий

// комментарий
```

Комментарий продолжается до конца строки.

Примеры:

```text
from(billing/orders) // источник

where(status = done) # только завершённые
```

Комментарии разрешены внутри многострочных директив:

```text
select(
    id,
    status, // статус
    amount,
)
```

Комментарии внутри строк не распознаются:

```text
where(url = "https://example.com/a//b")
where(tag = "#important")
```

---

# Поддерживаемые директивы

```text
from(...)
select(...)
where(...)
order(...)
limit(...)
offset(...)
```

Ограничения:

```text
from      не более одной
select    любое количество
where     любое количество
order     любое количество
limit     не более одной
offset    не более одной
```

Все `where(...)` объединяются через `AND`.

Источник обязателен, но может быть задан либо директивой `from(...)`, либо
аргументом `"from"` при вызове парсера.

---

# from

Источник задаётся путём:

```text
from(users)

from(billing/orders)

from(system/archive/orders)
```

Разделитель:

```text
/
```

---

# select

Поля перечисляются через запятую:

```text
select(
    id,
    status,
    amount
)
```

Вложенные поля:

```text
user.name

user.profile.avatar

parent.icon
```

Разделитель вложенности:

```text
.
```

---

# Константы

Поддерживаются JSON-константы:

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

Массив является альтернативной записью списка значений условия. Вложенные
массивы разрешены и рекурсивно приводятся к плоскому списку:

```text
where(matrix = [[1, 2], [3, [4, 5]]])
```

После разбора:

```go
Values: []any{int64(1), int64(2), int64(3), int64(4), int64(5)}
```

Каждый элемент итогового `Condition.Values` является примитивом.

Также поддерживается сокращённая запись строк:

```text
done

canceled

guest

moderator

GB
```

Сокращённая запись эквивалентна строковой:

```text
where(status = done, canceled)
```

эквивалентно:

```text
where(status = "done", "canceled")
```

эквивалентно:

```text
where(status = ["done", "canceled"])
```

Парсер всегда преобразует сокращённую запись в строку.

Позже строки могут быть преобразованы в числовые идентификаторы на основании метаданных поля.

Например:

```text
"done"     → 111

"canceled" → 222
```

---

# where

Общий вид:

```text
where(field operator values)
```

Примеры:

```text
where(status = done)

where(status = done, canceled)

where(status = ["done", "canceled"])

where(amount > 1)

where(parent = null)

where(parent != null)
```

---

# Операторы

Поддерживаются:

```text
=

!=

>

>=

<

<=

~

in

not in
```

Семантика:

```text
in      == =

not in  == !=
```

После разбора:

```text
in      → =

not in  → !=
```

---

# Полнотекстовый поиск

Для полнотекстового поиска используется оператор `~`:

```text
where(description ~ "canceled by client request")
```

Оператор принимает ровно одно значение. После разбора он сохраняется в
`Condition.Op` без преобразования:

```go
Condition{
    Field:  Field("description"),
    Op:     "~",
    Values: []any{"canceled by client request"},
}
```

Несколько значений или массив недопустимы:

```text
where(description ~ "one", "two")
where(description ~ ["one", "two"])
```

---

# Равенство множеству

Одиночное значение:

```text
where(id = 1)
```

Множество:

```text
where(id = 1, 2, 3)
```

Эквивалентная запись:

```text
where(id = [1, 2, 3])
```

Семантика:

```text
id ∈ {1,2,3}
```

---

# Неравенство множеству

```text
where(id != 1)
```

```text
where(id != 1, 2, 3)
```

```text
where(id != [1, 2, 3])
```

Семантика:

```text
id ∉ {1,2,3}
```

---

# Пустое множество

```text
where(id = [])
```

всегда ложно.

Условие сохраняется в `Query.Where` с пустым `Values`.

Метод `Query.IsEmpty()` возвращает `true`, если находит условие с
оператором `=` и пустым списком значений.

---

```text
where(id != [])
```

всегда истинно.

Условие также сохраняется в `Query.Where`. Сам парсер не оптимизирует и не
удаляет условия.

---

# Null

Проверка на отсутствие значения:

```text
where(parent = null)
```

Проверка на наличие:

```text
where(parent != null)
```

---

# Операторы порядка

Поддерживаются:

```text
>

>=

<

<=
```

Допускается только одно значение:

Корректно:

```text
where(amount > 10)

where(ts >= 1000)
```

Некорректно:

```text
where(amount > 1, 2)

where(amount > [1, 2])
```

---

# order

Пример:

```text
order(
    user.type asc,
    ts desc
)
```

Направления:

```text
asc

desc
```

Если направление не указано:

```text
order(user.name)
```

эквивалентно:

```text
order(user.name asc)
```

---

# limit

```text
limit(100)
```

Допускаются только положительные целые числа:

```text
limit(100)

limit(1000)
```

Значение `0` зарезервировано в `Query.Limit` для обозначения отсутствующей
директивы, поэтому `limit(0)` считается ошибкой.

---

# offset

```text
offset(200)
```

Допускаются неотрицательные целые числа до `MaxUint64` и строки:

```text
offset(0)

offset(200)

offset("next-page")
```

Числовой `Query.Offset` имеет тип `int64` в его диапазоне и `uint64` для
больших значений. Допустимый диапазон при выполнении запроса зависит от драйвера.

---

# Trailing comma

Разрешается:

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

Парсер только разбирает и проверяет запрос. Построение SQL в него не входит.

Основная функция:

```go
type Args map[string]string

func Parse(src string, args ...Args) (*Query, error)
```

`from(...)` можно не указывать в тексте запроса, если источник передан в
аргументах парсера:

```go
query, err := Parse(`select(id, user.name)`, Args{"from": "billing/orders"})
```

Явная директива `from(...)` имеет приоритет над аргументом `"from"`. Если
источник не указан ни в запросе, ни в аргументах, парсер возвращает ошибку.

Структура результата:

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

`From` хранится одной строкой с разделителем `/`:

```go
query.From // "billing/orders"
```

Для получения отдельных частей используется:

```go
func (q *Query) FromParts() []string
```

Пример результата:

```go
[]string{"billing", "orders"}
```

Поле также хранится одной строкой с разделителем `.`:

```go
field := Field("user.profile.avatar")
field.String() // "user.profile.avatar"
field.Parts()  // []string{"user", "profile", "avatar"}
```

Доступные методы `Field`:

```go
func (f Field) String() string
func (f Field) Parts() []string
```

`Condition.Values` имеет тип `[]any`. Типы его элементов после разбора:

```text
целое в диапазоне int64              int64
целое выше MaxInt64 до MaxUint64     uint64
прочие числа, включая дроби          json.Number
строка                               string
true/false                           bool
null                                 nil
```

Числа сохраняются без округления. Значения `json.Number` хранят исходную
запись числа. Тип поля определяет дальнейшее приведение значения.

Вложенные массивы из текста запроса рекурсивно разворачиваются. Вложенные
`[]any` в итоговом `Condition.Values` не сохраняются.

Сокращённые строковые значения, например `done`, сохраняются как `string`.

---

# Проверка пустого результата

В `Query` нет отдельного поля состояния пустого результата.

Для проверки используется вычисляемый метод:

```go
func (q *Query) IsEmpty() bool
```

Он просматривает `Where` и возвращает `true` при наличии условия вида:

```text
where(field = [])
```

Для `nil`-указателя на `Query` метод возвращает `false`.

---

# Преобразование обратно в xquery

Метод `String()` возвращает каноническую однострочную запись:

```go
func (q *Query) String() string
```

Пример:

```text
from(billing/orders) select(id, user.name) where(status = "done") order(ts desc) limit(10) offset("next-page")
```

Правила канонической записи:

- директивы выводятся в порядке `from`, `select`, `where`, `order`, `limit`, `offset`;
- все строковые значения выводятся как JSON-строки;
- направление `asc` не выводится, потому что оно используется по умолчанию;
- пустой список значений выводится как `[]`;
- для `nil`-указателя на `Query` возвращается пустая строка.

Поддерживается преобразование с сохранением структуры:

```text
Parse → Query.String → Parse
```

Комментарии и исходное форматирование при этом не сохраняются.

---

# JSON

`Query`, `Condition` и `Order` поддерживают стандартное преобразование через
`encoding/json`.

Пример:

```json
{
  "from": "billing/orders",
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

Поле `from` выводится всегда. Пустые `select`, `where`, `order`, `limit` и
`offset` не попадают в JSON благодаря `omitempty`.

---

# Пробелы

Эквивалентны:

```text
where(amount>1)
```

```text
where(amount > 1)
```

Эквивалентны:

```text
where(status=done,canceled)
```

```text
where(status = done, canceled)
```

---

# Нормализация

Парсер выполняет следующие преобразования.

Сокращённая строка:

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

Массив:

```text
where(id = [1, 2, 3])
```

↓

```go
Values: []any{int64(1), int64(2), int64(3)}
```

---

Пустое множество:

```text
where(id = [])
```

↓

```go
Values: nil
```

---

# Грамматика

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
