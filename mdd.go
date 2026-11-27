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
	app := new(A)
	core := any(app).(interface{ applicationCore() *application }).applicationCore()
	core.owner = app
	return app, core.Open(cfg)
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
	model := cfg.LookupModel(name)
	if model == nil {
		panic(fmt.Errorf("meta: unknown model %q", name))
	}
	return schemaByConfig(cfg).registerObjectType(model, reflect.TypeFor[T]())
}

type registrationTarget interface {
	meta.Node
	ModelType() reflect.Type
	Register(reflect.Type) struct{}
}

func (s *schema) registerObjectType(target registrationTarget, typ reflect.Type) struct{} {
	if s.prepared {
		if target.ModelType() == typ {
			return struct{}{}
		}
		panic(fmt.Errorf("mdd: register %q before opening the configuration", target.Path()))
	}
	must(s.factoryFor(target, typ))
	return target.Register(typ)
}

// RegisterEnum associates a Go enum with metadata in cfg before opening it.
func RegisterEnum[T any](cfg meta.Config, path string) struct{} {
	if cfg == nil {
		panic("mdd: metadata is nil")
	}
	enum := cfg.LookupEnum(path)
	if enum == nil {
		panic(fmt.Errorf("meta: unknown enum %q", path))
	}
	return schemaByConfig(cfg).registerObjectType(enum, reflect.TypeFor[T]())
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
	core := ctx.contextCore()
	return &Set[T]{set: newObjectSet(core, core.schema.metaTableByType(reflect.TypeFor[T]()))}
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
	set := NewSet[T](ctx)
	set.objectSet().withSQLQuery(statement, args)
	return set
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
	owner := node.Parent()
	if kind := owner.Kind(); kind != meta.KindModel && kind != meta.KindEnum {
		panic(fmt.Errorf("mdd: %q is not a model or enum item", path))
	}
	item := node.(meta.Item)
	target := owner.(registrationTarget)
	typ := reflect.TypeFor[T]()
	s := schemaByConfig(cfg)
	if existing := target.ModelType(); existing == nil {
		s.registerObjectType(target, typ)
	} else if existing != typ {
		panic(fmt.Errorf("mdd: %s is registered as %v, not %v", target.Path(), existing, typ))
	}
	id := storedScalar[uint64](item.ItemID(), item.Path())
	factory := must(s.factoryFor(target, typ))
	return any(s.predefinedObject(target, item, id, factory)).(*T)
}

// ID returns the numeric ID, or zero for a nil object or enum item.
func ID[P interface {
	*S
	objectValue
}, S any](obj P) uint64 {
	if obj == nil {
		return 0
	}
	return obj.objectCore().ID()
}

// Is reports whether a non-nil object matches any supplied ID or object.
func Is(obj objectValue, values ...any) bool {
	return !nilModel(obj) && obj.objectCore().Is(values...)
}

// Ctx returns the model's context, or nil for a nil model.
func Ctx[C any, P interface {
	*S
	objectValue
}, S any](obj P) *C {
	if obj == nil {
		return nil
	}
	ctx := obj.objectCore().MDDContext()
	if ctx == nil {
		return nil
	}
	return ctx.(*C)
}

// Attr reads a model attribute as T, returning the zero value for a nil model
// or an absent value.
func Attr[T any](obj interface {
	getValue(string) (any, *metaAttribute, *Context)
}, name string) (_ T) {
	if nilModel(obj) {
		return
	}
	val, attr, ctx := obj.getValue(name)
	if val == nil {
		return
	}
	if v, ok := convertPrimitive[T](val, name); ok {
		return v
	}
	converted := convertAttribute(val, attr, ctx, reflect.TypeFor[T](), name).Interface()
	if converted == nil {
		return
	}
	return converted.(T)
}

// AttrSet returns the typed set described by an object's set attribute.
// It defaults to the object's context. Predefined objects require an active context.
func AttrSet[T any](obj Object, name string, contexts ...ContextProvider) *Set[T] {
	if nilModel(obj) {
		return nil
	}
	owner := obj.objectCore()
	ctx := owner.ctx
	switch len(contexts) {
	case 0:
	case 1:
		ctx = nil
		if !nilModel(contexts[0]) {
			ctx = contexts[0].contextCore()
		}
	default:
		panic("mdd: AttrSet accepts at most one context")
	}
	attr := ctx.setAttribute(owner, name)
	set := NewSet[T](ctx)
	set.objectSet().applyAttrQuery(attr, owner.self)
	return set
}

func nilModel(obj any) bool {
	value := reflect.ValueOf(obj)
	return !value.IsValid() || value.Kind() == reflect.Pointer && value.IsNil()
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
