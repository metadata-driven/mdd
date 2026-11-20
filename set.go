package mdd

import (
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strings"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/meta"
)

type Set[T any] struct {
	ctx   *Context
	tab   *metaTable
	query *driver.Query
}

// ObjectSet is a dynamically selected set of model objects.
type ObjectSet = Set[Object]

// NewSet returns a set by model name or set name.
func (ctx *Context) NewSet(name string) *ObjectSet {
	if ctx == nil {
		return nil
	}
	tab := ctx.schema.tables[name]
	if tab == nil || tab.Table == nil {
		panic(fmt.Errorf("mdd: unknown model set %q", name))
	}
	return &ObjectSet{ctx: ctx, tab: tab, query: &driver.Query{Table: tab.Table.Name}}
}

// Model returns the metadata for this set's model.
func (s *Set[T]) Model() meta.Model {
	if s == nil {
		return nil
	}
	return s.tab.Model
}

func (s *Set[T]) init(ctx *Context) {
	s.ctx = ctx
	s.tab = ctx.metaTableByType(reflect.TypeFor[T]())
	s.query = &driver.Query{Table: s.tab.Table.Name}
}

func (s *Set[T]) Get(id uint64) *T {
	if s == nil {
		return nil
	}
	if id == 0 {
		panic("invalid id")
	}
	return s.newObject(id)
}

func (s *Set[T]) GetObject(id uint64) any {
	obj := s.Get(id)
	if obj == nil {
		return nil
	}
	return obj
}

func (s *Set[T]) Add() *T {
	if s == nil {
		return nil
	}
	return s.modelObject(0)
}

func (s *Set[T]) newObject(id uint64) *T {
	if s == nil || id == 0 {
		return nil
	}
	return s.modelObject(id)
}

func (s *Set[T]) modelObject(id uint64) *T {
	obj := s.ctx.newObject(s.tab, id)
	if value, ok := any(obj).(*T); ok {
		return value
	}
	return obj.self.(*T)
}

// Delete removes the objects selected by the current query.
func (s *Set[T]) Delete() {
	if s == nil {
		return
	}
	s.ctx.checkWritable()
	var ids []uint64
	s.ctx.txFetch(s.tab, s.copyQuery(), func(id uint64) error {
		ids = append(ids, id)
		return nil
	})
	for _, id := range ids {
		s.ctx.newObject(s.tab, id).Delete()
	}
}

func (s *Set[T]) copyQuery() *driver.Query {
	q := *s.query
	q.Joins = slices.Clone(s.query.Joins)
	q.Filters = slices.Clone(s.query.Filters)
	q.Order = slices.Clone(s.query.Order)
	return &q
}

func (s *Set[T]) IDs() (ids []uint64) {
	for obj := range s.All() {
		ids = append(ids, any(obj).(interface{ ID() uint64 }).ID())
	}
	return
}

// All executes the query when iteration begins.
func (s *Set[T]) All() iter.Seq[*T] {
	if s == nil {
		return func(func(*T) bool) {}
	}
	query := s.copyQuery()
	return func(yield func(*T) bool) {
		s.ctx.txFetch(s.tab, query, func(id uint64) error {
			if !yield(s.newObject(id)) {
				return driver.ErrBreak
			}
			return nil
		})
	}
}

// Fetch calls fn for each object in the query and returns the set.
func (s *Set[T]) Fetch(fn func(*T)) *Set[T] {
	for obj := range s.All() {
		fn(obj)
	}
	return s
}

// Query adds conditions and ordering from an xquery expression.
// Nonzero limits and nonempty offsets replace the current values.
func (s *Set[T]) Query(expr string) *Set[T] {
	if s == nil {
		return nil
	}
	q := s.ctx.parseQuery(expr, s.tab.Table.Name, s.ctx.getSelf())
	addQueryJoins(s.query, q.Joins...)
	s.query.Filters = append(s.query.Filters, q.Filters...)
	s.query.Order = append(s.query.Order, q.Order...)
	if q.Limit != 0 {
		s.query.Limit = q.Limit
	}
	if q.Offset != "" {
		s.query.Offset = q.Offset
	}
	return s
}

// Where adds a condition to the query. Multiple conditions are combined with AND.
func (s *Set[T]) Where(name, op string, values ...any) *Set[T] {
	if s == nil {
		return nil
	}
	filter := s.ctx.schema.queryFilter(s.tab, s.query, name, op, values)
	s.query.Filters = append(s.query.Filters, filter)
	return s
}

// Filter adds an equality condition. Multiple values are alternatives.
func (s *Set[T]) Filter(name string, values ...any) *Set[T] {
	return s.Where(name, driver.OpEqual, values...)
}

// WhereContainsAll matches collections containing every supplied value.
func (s *Set[T]) WhereContainsAll(name string, values ...any) *Set[T] {
	return s.Where(name, OpContainsAll, values...)
}

// WhereContainsAny matches collections containing at least one supplied value.
func (s *Set[T]) WhereContainsAny(name string, values ...any) *Set[T] {
	return s.Where(name, OpContainsAny, values...)
}

// Order sets the sort columns. A leading minus sign selects descending order.
func (s *Set[T]) Order(names ...string) *Set[T] {
	if s == nil {
		return nil
	}
	order := make([]driver.QueryOrder, 0, len(names))
	for _, name := range names {
		name, desc := strings.CutPrefix(name, "-")
		if name == "" {
			panic("mdd: empty order column")
		}
		source, column, _ := s.ctx.schema.resolveQueryField(s.tab, s.query, name)
		order = append(order, driver.QueryOrder{Source: source, Column: column, Desc: desc})
	}
	s.query.Order = order
	return s
}

// Limit sets the maximum number of results. Zero removes the limit.
func (s *Set[T]) Limit(n int64) *Set[T] {
	if s == nil {
		return nil
	}
	if n < 0 {
		panic(fmt.Errorf("mdd: invalid limit %d", n))
	}
	s.query.Limit = n
	return s
}

// Offset sets the continuation offset returned by the storage driver.
func (s *Set[T]) Offset(offset string) *Set[T] {
	if s == nil {
		return nil
	}
	s.query.Offset = offset
	return s
}

// Count returns the number of objects in the current query.
func (s *Set[T]) Count() (count int64) {
	if s == nil {
		return 0
	}
	query := s.copyQuery()
	s.ctx.txFetch(s.tab, query, func(uint64) error {
		count++
		return nil
	})
	return count
}

// Exists reports whether the current query contains at least one object.
func (s *Set[T]) Exists() bool {
	if s == nil {
		return false
	}
	return s.firstID(s.query) != 0
}

func (s *Set[T]) First() *T {
	if s == nil {
		return nil
	}
	return s.first(s.query)
}

func (s *Set[T]) Last() *T {
	if s == nil {
		return nil
	}
	query := s.copyQuery()
	var lastID uint64
	s.ctx.txFetch(s.tab, query, func(id uint64) error {
		lastID = id
		return nil
	})
	return s.newObject(lastID)
}

func (s *Set[T]) FindOrAdd() *T {
	if result := s.first(s.query); result != nil {
		return result
	}
	result := s.Add()
	object := any(result).(interface{ Set(string, any) })
	for _, filter := range s.query.Filters {
		if filter.Source == "" && filter.Column != "id" && (filter.Op == "" || filter.Op == driver.OpEqual) && len(filter.Values) > 0 {
			object.Set(filter.Column, filter.Values[0])
		}
	}
	return result
}

func (s *Set[T]) Find() *T {
	return s.First()
}

func (s *Set[T]) MustFind() *T {
	if result := s.First(); result != nil {
		return result
	}
	panic(ErrNotFound)
}

func (s *Set[T]) FindBy(name string, value any) *T {
	if s == nil {
		return nil
	}
	query := s.copyQuery()
	query.Filters = slices.Clone(query.Filters)
	filter := s.ctx.schema.queryFilter(s.tab, query, name, driver.OpEqual, []any{value})
	query.Filters = append(query.Filters, filter)
	return s.first(query)
}

func (s *Set[T]) first(query *driver.Query) *T {
	return s.newObject(s.firstID(query))
}

func (s *Set[T]) firstID(query *driver.Query) (result uint64) {
	q := *query
	q.Limit = 1
	s.ctx.txFetch(s.tab, &q, func(id uint64) error {
		result = id
		return driver.ErrBreak
	})
	return result
}
