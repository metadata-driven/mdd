package mdd

import (
	"fmt"
	"reflect"

	"github.com/metadata-driven/mdd/driver"
	_ "github.com/metadata-driven/mdd/driver/file"
	_ "github.com/metadata-driven/mdd/driver/json"
	_ "github.com/metadata-driven/mdd/driver/mem"
	_ "github.com/metadata-driven/mdd/driver/yaml"
	"github.com/metadata-driven/mdd/meta"
)

// ErrNotFound indicates that a requested record does not exist.
var ErrNotFound = driver.ErrNotFound

// NewApp creates an application of type A and opens it with cfg.
func NewApp[A any](cfg meta.Config) (*A, error) {
	a := newBaseObject(reflect.TypeFor[A]())
	err := a.(appProvider).Open(cfg)
	return a.getSelf().(*A), err
}

// App returns the application that owns the context, or nil for a nil context.
func App[A any, P interface {
	*S
	MDDApp() any
}, S any](ctx P) *A {
	if ctx == nil {
		return nil
	}
	app := ctx.MDDApp()
	if app == nil {
		return nil
	}
	return app.(*A)
}

// RegisterModel associates a Go model with metadata in cfg and panics on error.
func RegisterModel[T any](cfg meta.Config, name string) struct{} {
	if cfg == nil {
		panic("mdd: metadata is nil")
	}
	model, ok := any((*T)(nil)).(meta.ModelObject)
	if !ok {
		panic(fmt.Errorf("mdd: model type %v must implement MDDObject", reflect.TypeFor[T]()))
	}
	return cfg.ModelByName(name).Register(model)
}

// RegisterEnum associates a Go enum with metadata in cfg and panics on error.
func RegisterEnum[T any](cfg meta.Config, path string) struct{} {
	typ := reflect.TypeFor[T]()
	if typ.Kind() != reflect.Struct || !reflect.PointerTo(typ).Implements(reflect.TypeFor[interface{ enumItemCore() *EnumItem }]()) {
		panic(fmt.Errorf("mdd: enum type %v must embed mdd.EnumItem", typ))
	}
	check(cfg.RegisterEnum(path, typ))
	return struct{}{}
}

// NewSet creates a set for the registered model type T in ctx.
// It returns nil when ctx is nil.
func NewSet[T any, P interface {
	*S
	contextCore() *Context
}, S any](ctx P) *Set[T] {
	if ctx == nil {
		return nil
	}
	s := new(Set[T])
	s.init(ctx.contextCore())
	return s
}

// NewQuery creates a set and applies an xquery expression.
func NewQuery[T any, P interface {
	*S
	contextCore() *Context
}, S any](ctx P, query string) *Set[T] {
	return NewSet[T](ctx).Query(query)
}

// NewSQLQuery creates a set for T using a driver-native SQL query.
// Supply a trusted read query returning one column of IDs from T's primary
// table. Express filtering, ordering and offsets in the statement.
func NewSQLQuery[T any, P interface {
	*S
	contextCore() *Context
}, S any](ctx P, statement string, args ...any) *Set[T] {
	return NewSet[T](ctx).withSQLQuery(statement, args)
}

// Item returns the shared predefined model or enumeration item at path.
func Item[T any](cfg meta.Config, path string) *T {
	if cfg == nil {
		panic("mdd: metadata is nil")
	}
	node := cfg.Lookup(path)
	if node == nil || node.Kind() != meta.KindItem || node.Parent() == nil {
		panic(fmt.Errorf("mdd: %q is not a predefined item", path))
	}
	item := node.(meta.Item)
	typ := reflect.TypeFor[T]()
	if typ.Kind() != reflect.Struct {
		panic(fmt.Errorf("mdd: invalid model type %v", typ))
	}
	if node.Parent().Kind() == meta.KindEnum {
		enum := node.Parent().(meta.Enum)
		if existing := enum.EnumType(); existing == nil {
			RegisterEnum[T](cfg, enum.Path())
		} else if existing != typ {
			panic(fmt.Errorf("mdd: %s is registered as %v, not %v", enum.Path(), existing, typ))
		}
		return predefinedEnumItem(enum, typ, item).object.self.(*T)
	}
	if node.Parent().Kind() != meta.KindModel {
		panic(fmt.Errorf("mdd: %q is not a model or enum item", path))
	}
	model := node.Parent().(meta.Model)
	if existing := model.ModelType(); existing == nil {
		candidate, ok := any((*T)(nil)).(meta.ModelObject)
		if !ok {
			panic(fmt.Errorf("mdd: model type %v does not expose an MDD object", typ))
		}
		model.Register(candidate)
	} else if existing != typ {
		panic(fmt.Errorf("mdd: %s is registered as %v, not %v", model.Path(), existing, typ))
	}
	id := storedScalar[uint64](item.ItemID(), item.Path())
	if obj := model.Predefined(id); obj != nil {
		return any(obj).(*T)
	}
	s := must(convertSchema(cfg))
	s.textTemplates.parser = TemplateEngine.Text
	s.queryTemplates.parser = TemplateEngine.Query
	check(s.warmTemplates(cfg))
	tab := s.tables[model.Path()]
	if tab == nil || tab.predefined[id] == nil {
		panic(fmt.Errorf("mdd: invalid predefined item %s", path))
	}
	return predefinedObject(s, tab, item).self.(*T)
}

// ID returns the model ID, or zero for a nil model.
func ID[P interface {
	*S
	MDDObject() any
}, S any](obj P) uint64 {
	if obj == nil {
		return 0
	}
	return obj.MDDObject().(*Object).ID()
}

// Is reports whether a non-nil object matches any supplied ID or object.
func Is[P interface {
	*S
	objectKeyProvider
}, S any](obj P, values ...any) bool {
	if obj == nil {
		return false
	}
	return isValues(obj, values)
}

// Ctx returns the model's context, or nil for a nil model.
func Ctx[C any, P interface {
	*S
	MDDObject() any
}, S any](obj P) *C {
	if obj == nil {
		return nil
	}
	ctx := obj.MDDObject().(*Object).MDDContext()
	if ctx == nil {
		return nil
	}
	return ctx.(*C)
}

// Attr reads a model attribute as T, returning the zero value for a nil model
// or an absent value.
func Attr[T any, P interface {
	*S
	getValue(string) (any, *metaAttribute, *Context)
}, S any](obj P, name string) (_ T) {
	if obj == nil {
		return
	}
	val, attr, ctx := obj.getValue(name)
	if val == nil {
		return
	}
	if v, ok := convertPrimitive[T](val, name); ok {
		return v
	}
	return convertAttribute(val, attr, ctx, reflect.TypeFor[T](), name).Interface().(T)
}

// AttrSet returns the set described by an object's set attribute.
func AttrSet[T any, P interface {
	*S
	setAttribute(string) (*Context, *metaAttribute, any)
}, S any](obj P, name string) *Set[T] {
	if obj == nil {
		return nil
	}
	ctx, attr, self := obj.setAttribute(name)
	set := NewSet[T](ctx)
	if attr.Type().Target.Path() != set.tab.Model.Path() {
		panic("mdd: set attribute " + attr.Path() + " does not match model " + set.tab.Model.Path())
	}
	set.query = ctx.schema.parseQuery(attr.Query(), set.tab.Table.Name, self)
	return set
}

// SetAttr updates an attribute and returns the model. A nil model stays nil.
func SetAttr[P interface {
	*S
	Set(string, any)
}, S any](object P, name string, value any) P {
	if object != nil {
		object.Set(name, value)
	}
	return object
}
