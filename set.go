package mdd

import (
	"iter"
	"slices"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/meta"
)

// Set is a typed view of a model query.
type Set[T any] struct {
	set ObjectSet
}

func (s *Set[T]) objectSet() *ObjectSet {
	if s == nil {
		return nil
	}
	return &s.set
}

func typedObject[T any](obj *object) *T {
	if obj == nil {
		return nil
	}
	return any(obj.self).(*T)
}

// Model returns the metadata for this set's model.
func (s *Set[T]) Model() meta.Model {
	return s.objectSet().Model()
}

func (s *Set[T]) Get(id uint64) *T {
	return typedObject[T](s.objectSet().get(id))
}

func (s *Set[T]) GetObject(id uint64) any {
	obj := s.Get(id)
	if obj == nil {
		return nil
	}
	return obj
}

func (s *Set[T]) Add() *T {
	return typedObject[T](s.objectSet().add())
}

// Delete removes the objects selected by the current query.
func (s *Set[T]) Delete() {
	s.objectSet().Delete()
}

func (s *Set[T]) IDs() []uint64 {
	return slices.Collect(mapSeq(s.objectSet().all(), func(obj *object) uint64 {
		return obj.modelObject().ID()
	}))
}

// All executes the query when iteration begins.
func (s *Set[T]) All() iter.Seq[*T] {
	return mapSeq(s.objectSet().all(), typedObject[T])
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
	s.objectSet().Query(expr)
	return s
}

// Where adds a condition to the query. Multiple conditions are combined with AND.
func (s *Set[T]) Where(name, op string, values ...any) *Set[T] {
	s.objectSet().Where(name, op, values...)
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
	s.objectSet().Order(names...)
	return s
}

// Limit sets the maximum number of results. Zero removes the limit.
func (s *Set[T]) Limit(n int64) *Set[T] {
	s.objectSet().Limit(n)
	return s
}

// Offset sets the continuation offset returned by the storage driver.
func (s *Set[T]) Offset(offset string) *Set[T] {
	s.objectSet().Offset(offset)
	return s
}

// WhereSQL adds a driver-native WHERE predicate, combined with AND.
// Reference the primary table as j0 and bind values with ? placeholders.
func (s *Set[T]) WhereSQL(predicate string, args ...any) *Set[T] {
	s.objectSet().WhereSQL(predicate, args...)
	return s
}

// Count returns the number of objects in the current query.
func (s *Set[T]) Count() int64 {
	return s.objectSet().Count()
}

// Exists reports whether the current query contains at least one object.
func (s *Set[T]) Exists() bool {
	return s.objectSet().Exists()
}

func (s *Set[T]) First() *T {
	return typedObject[T](s.objectSet().first())
}

func (s *Set[T]) Last() *T {
	return typedObject[T](s.objectSet().last())
}

func (s *Set[T]) FindOrAdd() *T {
	return typedObject[T](s.objectSet().findOrAdd())
}

func (s *Set[T]) Find() *T {
	return s.First()
}

func (s *Set[T]) MustFind() *T {
	return typedObject[T](s.objectSet().mustFind())
}

func (s *Set[T]) FindBy(name string, value any) *T {
	return typedObject[T](s.objectSet().findBy(name, value))
}
