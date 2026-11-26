package meta

import (
	"fmt"
	"reflect"
)

func (o *node) LookupModel(name string) Model {
	if node := o.LookupType(name); node != nil && node.Kind() == KindModel {
		return node.(Model)
	}
	return nil
}

func (o *node) LookupEnum(path string) Enum {
	if node := o.LookupType(path); node != nil && node.Kind() == KindEnum {
		return node.(Enum)
	}
	return nil
}

func (o *node) Register(object ModelObject) struct{} {
	if o.kind != KindModel && o.kind != KindEnum {
		panic(fmt.Errorf("meta: %q is not a model or enum", o.path))
	}
	typ := reflect.TypeOf(object)
	if typ == nil || typ.Kind() != reflect.Pointer || typ.Elem().Kind() != reflect.Struct {
		panic(fmt.Errorf("meta: %q requires a pointer to a struct", o.path))
	}
	typ = typ.Elem()
	if o.modelType == typ {
		return struct{}{}
	}
	if o.modelType != nil {
		panic(fmt.Errorf("meta: %q already registered as %v", o.path, o.modelType))
	}
	registry := o.root.modelByGoType
	if previous, found := registry[typ]; found {
		if o.kind == KindEnum || previous != nil && previous.kind == KindEnum {
			if previous == nil {
				panic(fmt.Errorf("meta: Go type %v is already registered by multiple models", typ))
			}
			panic(fmt.Errorf("meta: Go type %v already registered as %s", typ, previous.path))
		}
		// Regular models can share a Go type. Lookup by Go type is ambiguous.
		registry[typ] = nil
	} else {
		registry[typ] = o
	}
	o.modelType = typ
	return struct{}{}
}

func (o *node) ModelType() reflect.Type { return o.modelType }

func (o *node) ModelByType(typ reflect.Type) Model {
	if model := o.root.modelByGoType[typ]; model != nil && model.kind == KindModel {
		return model
	}
	return nil
}

func (o *node) EnumByType(typ reflect.Type) Enum {
	if enum := o.root.modelByGoType[typ]; enum != nil && enum.kind == KindEnum {
		return enum
	}
	return nil
}
