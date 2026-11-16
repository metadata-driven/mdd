package meta

import (
	"fmt"
	"reflect"
)

func (o *node) ModelByName(name string) Model {
	item := o.Lookup(name)
	if item == nil || item.Kind() != KindModel || item.Path() != name {
		panic(fmt.Errorf("meta: unknown model %q", name))
	}
	return item.(Model)
}

func (o *node) Register(model ModelObject) struct{} {
	typ := reflect.TypeOf(model)
	if typ == nil || typ.Kind() != reflect.Pointer || typ.Elem().Kind() != reflect.Struct {
		panic(fmt.Errorf("meta: model %q requires a pointer to a struct", o.path))
	}
	typ = typ.Elem()
	if old := o.modelType; old != nil && old != typ {
		panic(fmt.Errorf("meta: model %q already registered as %v", o.path, old))
	}
	o.predefined.Range(func(_, value any) bool {
		if old := reflect.TypeOf(value); old != reflect.PointerTo(typ) {
			panic(fmt.Errorf("meta: model %q already has predefined objects of type %v", o.path, old))
		}
		return true
	})
	o.modelType = typ
	return struct{}{}
}

func (o *node) ModelType() reflect.Type {
	return o.modelType
}

func (o *node) Predefined(id uint64) ModelObject {
	if value, ok := o.predefined.Load(id); ok {
		return value.(ModelObject)
	}
	return nil
}

func (o *node) CachePredefined(id uint64, obj ModelObject) ModelObject {
	value, _ := o.predefined.LoadOrStore(id, obj)
	return value.(ModelObject)
}

func (o *node) RegisterEnum(path string, typ reflect.Type) error {
	if typ == nil || typ.Kind() != reflect.Struct {
		return fmt.Errorf("meta: enum %q requires a struct type", path)
	}
	item := o.Lookup(path)
	if item == nil || item.Kind() != KindEnum {
		return fmt.Errorf("meta: unknown enum %q", path)
	}
	if old := o.root.enumTypes[typ]; old != nil && old.Path() != path {
		return fmt.Errorf("meta: enum type %v already registered as %s", typ, old.Path())
	}
	enum := item.(*node)
	if old := enum.enumType; old != nil && old != typ {
		return fmt.Errorf("meta: enum %s already registered as %v", path, old)
	}
	enum.enumType = typ
	o.root.enumTypes[typ] = item.(Enum)
	return nil
}

func (o *node) EnumType() reflect.Type { return o.enumType }

func (o *node) EnumByType(typ reflect.Type) Enum {
	return o.root.enumTypes[typ]
}
