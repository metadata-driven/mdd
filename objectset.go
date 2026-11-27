package mdd

import (
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/meta"
)

// ObjectSet queries one model selected at runtime.
type ObjectSet struct {
	ctx   *Context
	tab   *metaTable
	query *driver.Query
}

// NewSet returns a set by model name or set name.
func (ctx *Context) NewSet(name string) *ObjectSet {
	if ctx == nil {
		return nil
	}
	tab := ctx.schema.tables[name]
	if tab == nil || tab.Table == nil {
		panic(fmt.Errorf("mdd: unknown model set %q", name))
	}
	set := newObjectSet(ctx, tab)
	return &set
}

// NewAttrSet creates a set from the owner's attribute and queries it in ctx.
// Predefined owners can be used with an active context from the same configuration.
func (ctx *Context) NewAttrSet(obj Object, name string) *ObjectSet {
	if nilModel(obj) {
		return nil
	}
	owner := obj.objectCore()
	attr := ctx.setAttribute(owner, name)
	set := ctx.NewSet(attr.Type().Target.Path())
	set.applyAttrQuery(attr, owner.self)
	return set
}

func (ctx *Context) setAttribute(owner *object, name string) *metaAttribute {
	if owner == nil || owner.tab == nil {
		panic("mdd: set attribute owner has no initialized model")
	}
	attr := owner.tab.Attrs[name]
	if attr == nil {
		panic(fmt.Errorf("mdd: unknown attribute %s.%s", owner.tab.Name, name))
	}
	if !attr.Type().Set {
		panic(fmt.Errorf("mdd: %s is not a set", attr.Path()))
	}
	if ctx == nil || ctx.app == nil {
		panic(fmt.Errorf("mdd: %s requires an active transaction context", attr.Path()))
	}
	if owner.ctx != ctx && owner.ctx != ctx.schema.ctx {
		panic(fmt.Errorf("mdd: %s requires the owner's transaction context", attr.Path()))
	}
	return attr
}

func (s *ObjectSet) applyAttrQuery(attr *metaAttribute, owner any) {
	if attr.Type().Target.Path() != s.tab.Model.Path() {
		panic("mdd: set attribute " + attr.Path() + " does not match model " + s.tab.Model.Path())
	}
	s.query = s.ctx.schema.parseQuery(attr.Query(), s.tab.Table.Name, owner)
}

// NewSQLQuery creates a model set selected by a driver-native SQL query.
// The query must return one column of model IDs.
func (ctx *Context) NewSQLQuery(name, statement string, args ...any) *ObjectSet {
	return ctx.NewSet(name).withSQLQuery(statement, args)
}

// Model returns the metadata for this set's model.
func (s *ObjectSet) Model() meta.Model {
	if s == nil {
		return nil
	}
	return s.tab.Model
}

func newObjectSet(ctx *Context, tab *metaTable) ObjectSet {
	return ObjectSet{ctx: ctx, tab: tab, query: &driver.Query{Table: tab.Table.Name}}
}

func (s *ObjectSet) Get(id uint64) Object {
	return s.get(id).modelObject()
}

func (s *ObjectSet) get(id uint64) *object {
	if s == nil {
		return nil
	}
	if id == 0 {
		panic("invalid id")
	}
	return s.ctx.newObject(s.tab, id)
}

func (s *ObjectSet) GetObject(id uint64) any {
	return s.Get(id)
}

func (s *ObjectSet) Add() Object {
	return s.add().modelObject()
}

func (s *ObjectSet) add() *object {
	if s == nil {
		return nil
	}
	return s.ctx.newObject(s.tab, 0)
}

func (s *ObjectSet) objectByID(id uint64) *object {
	if s == nil || id == 0 {
		return nil
	}
	return s.ctx.newObject(s.tab, id)
}

// Delete removes the objects selected by the current query.
func (s *ObjectSet) Delete() {
	if s == nil {
		return
	}
	s.ctx.checkWritable()
	ids := s.IDs()
	for _, id := range ids {
		s.ctx.newObject(s.tab, id).Delete()
	}
}

func (s *ObjectSet) copyQuery() *driver.Query {
	q := *s.query
	q.Joins = slices.Clone(s.query.Joins)
	q.Filters = slices.Clone(s.query.Filters)
	q.Order = slices.Clone(s.query.Order)
	q.SQLFilters = slices.Clone(s.query.SQLFilters)
	return &q
}

func (s *ObjectSet) IDs() []uint64 {
	return slices.Collect(s.seqIDs())
}

// All executes the query when iteration begins.
func (s *ObjectSet) All() iter.Seq[Object] {
	return mapSeq(s.all(), (*object).modelObject)
}

func (s *ObjectSet) seqIDs() iter.Seq[uint64] {
	if s == nil {
		return func(func(uint64) bool) {}
	}
	query := s.copyQuery()
	return func(yield func(uint64) bool) {
		s.ctx.txFetch(s.tab, query, func(id uint64) error {
			if !yield(id) {
				return driver.ErrBreak
			}
			return nil
		})
	}
}

func (s *ObjectSet) all() iter.Seq[*object] {
	return mapSeq(s.seqIDs(), s.objectByID)
}

// Fetch calls fn for each object in the query and returns the set.
func (s *ObjectSet) Fetch(fn func(Object)) *ObjectSet {
	for obj := range s.All() {
		fn(obj)
	}
	return s
}

// Query adds conditions and ordering from an xquery expression.
// Nonzero limits and nonempty offsets replace the current values.
func (s *ObjectSet) Query(expr string) *ObjectSet {
	if s == nil {
		return nil
	}
	q := s.ctx.schema.parseQuery(expr, s.tab.Table.Name, s.ctx.self)
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
func (s *ObjectSet) Where(name, op string, values ...any) *ObjectSet {
	if s == nil {
		return nil
	}
	filter := s.ctx.schema.queryFilter(s.tab, s.query, name, op, values)
	s.query.Filters = append(s.query.Filters, filter)
	return s
}

// Filter adds an equality condition. Multiple values are alternatives.
func (s *ObjectSet) Filter(name string, values ...any) *ObjectSet {
	return s.Where(name, driver.OpEqual, values...)
}

// WhereContainsAll matches collections containing every supplied value.
func (s *ObjectSet) WhereContainsAll(name string, values ...any) *ObjectSet {
	return s.Where(name, OpContainsAll, values...)
}

// WhereContainsAny matches collections containing at least one supplied value.
func (s *ObjectSet) WhereContainsAny(name string, values ...any) *ObjectSet {
	return s.Where(name, OpContainsAny, values...)
}

// Order sets the sort columns. A leading minus sign selects descending order.
func (s *ObjectSet) Order(names ...string) *ObjectSet {
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
func (s *ObjectSet) Limit(n int64) *ObjectSet {
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
func (s *ObjectSet) Offset(offset string) *ObjectSet {
	if s == nil {
		return nil
	}
	s.query.Offset = offset
	return s
}

// WhereSQL adds a driver-native WHERE predicate, combined with AND.
// Reference the primary table as j0 and bind values with ? placeholders.
func (s *ObjectSet) WhereSQL(predicate string, args ...any) *ObjectSet {
	if s == nil {
		return nil
	}
	s.query.SQLFilters = append(s.query.SQLFilters, newSQLExpression(predicate, args))
	return s
}

func (s *ObjectSet) withSQLQuery(statement string, args []any) *ObjectSet {
	if s == nil {
		return nil
	}
	expression := newSQLExpression(statement, args)
	s.query.RawSQL = &expression
	return s
}

func newSQLExpression(text string, args []any) driver.SQLExpression {
	if strings.TrimSpace(text) == "" {
		panic("mdd: empty SQL expression")
	}
	args = slices.Clone(args)
	for i, arg := range args {
		if data, ok := arg.([]byte); ok {
			args[i] = slices.Clone(data)
		}
	}
	return driver.SQLExpression{Text: text, Args: args}
}

// Count returns the number of objects in the current query.
func (s *ObjectSet) Count() (count int64) {
	for range s.seqIDs() {
		count++
	}
	return
}

// Exists reports whether the current query contains at least one object.
func (s *ObjectSet) Exists() bool {
	if s == nil {
		return false
	}
	return s.firstID(s.query) != 0
}

func (s *ObjectSet) First() Object {
	return s.first().modelObject()
}

func (s *ObjectSet) first() *object {
	if s == nil {
		return nil
	}
	return s.objectByID(s.firstID(s.query))
}

func (s *ObjectSet) Last() Object {
	return s.last().modelObject()
}

func (s *ObjectSet) last() *object {
	if s == nil {
		return nil
	}
	query := s.copyQuery()
	var lastID uint64
	s.ctx.txFetch(s.tab, query, func(id uint64) error {
		lastID = id
		return nil
	})
	return s.objectByID(lastID)
}

func (s *ObjectSet) FindOrAdd() Object {
	return s.findOrAdd().modelObject()
}

func (s *ObjectSet) findOrAdd() *object {
	if result := s.first(); result != nil {
		return result
	}
	result := s.add()
	if result == nil {
		return nil
	}
	for _, filter := range s.query.Filters {
		if filter.Source == "" && filter.Column != "id" && (filter.Op == "" || filter.Op == driver.OpEqual) && len(filter.Values) > 0 {
			result.modelObject().Set(filter.Column, cloneDataValue(filter.Values[0]))
		}
	}
	return result
}

func (s *ObjectSet) Find() Object {
	return s.First()
}

func (s *ObjectSet) MustFind() Object {
	return s.mustFind().modelObject()
}

func (s *ObjectSet) mustFind() *object {
	if result := s.first(); result != nil {
		return result
	}
	panic(ErrNotFound)
}

func (s *ObjectSet) FindBy(name string, value any) Object {
	return s.findBy(name, value).modelObject()
}

func (s *ObjectSet) findBy(name string, value any) *object {
	if s == nil {
		return nil
	}
	query := s.copyQuery()
	filter := s.ctx.schema.queryFilter(s.tab, query, name, driver.OpEqual, []any{value})
	query.Filters = append(query.Filters, filter)
	return s.objectByID(s.firstID(query))
}

func (s *ObjectSet) firstID(query *driver.Query) (result uint64) {
	q := *query
	q.Limit = 1
	s.ctx.txFetch(s.tab, &q, func(id uint64) error {
		result = id
		return driver.ErrBreak
	})
	return result
}
