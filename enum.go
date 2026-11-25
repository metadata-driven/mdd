package mdd

import (
	"fmt"
	"reflect"

	"github.com/metadata-driven/mdd/meta"
)

// EnumItem is a shared, immutable item of an enumeration.
type EnumItem struct{ object Object }

func (e *EnumItem) enumItemCore() *EnumItem { return e }

// MDDObject returns the internal object of an enumeration item.
func (e *EnumItem) MDDObject() any {
	if e == nil {
		return (*Object)(nil)
	}
	return &e.object
}

func (e *EnumItem) ID() uint64 {
	if e == nil {
		return 0
	}
	return e.object.ID()
}

func (e *EnumItem) objectKey() objectKey {
	if e == nil {
		return objectKey{}
	}
	return e.object.objectKey()
}

// Is reports whether this enum item matches any supplied ID or item.
func (e *EnumItem) Is(values ...any) bool {
	return e != nil && isValues(e, values)
}

func (e *EnumItem) Name() string {
	if e == nil || e.object.item == nil {
		return ""
	}
	return e.object.item.Name()
}

func (e *EnumItem) Title() string {
	if e == nil || e.object.item == nil {
		return ""
	}
	return e.object.item.Title()
}

func (e *EnumItem) String() string { return e.Title() }

func (e *EnumItem) Description() string {
	if e == nil || e.object.item == nil {
		return ""
	}
	return e.object.item.Description()
}

func (e *EnumItem) Icon() string {
	if e == nil || e.object.item == nil {
		return ""
	}
	return e.object.item.Icon()
}

func (e *EnumItem) Get(name string) any {
	if e == nil || e.object.item == nil {
		return nil
	}
	return enumItemValue(e.object.item, e.object.id, name)
}

func enumItemValue(item meta.Item, id uint64, name string) any {
	switch name {
	case "id":
		return id
	case "name":
		return item.Name()
	case "title":
		if value, ok := item.Raw()[name]; ok {
			return value
		}
		return item.Name()
	case "description", "icon":
		return item.Raw()[name]
	default:
		return cloneDataValue(item.Data()[name])
	}
}

func predefinedEnumItem(enum meta.Enum, typ reflect.Type, item meta.Item) *EnumItem {
	id := storedScalar[uint64](item.ItemID(), item.Path())
	if id == 0 {
		panic(fmt.Errorf("mdd: %s has an invalid zero ID", item.Path()))
	}
	if value := enum.Predefined(id); value != nil {
		return value.(interface{ enumItemCore() *EnumItem }).enumItemCore()
	}
	value := reflect.New(typ).Interface()
	model, ok := value.(meta.ModelObject)
	if !ok {
		panic(fmt.Errorf("mdd: enum type %v does not expose an MDD object", typ))
	}
	core, ok := value.(interface{ enumItemCore() *EnumItem })
	if !ok {
		panic(fmt.Errorf("mdd: enum type %v must embed mdd.EnumItem", typ))
	}
	obj := core.enumItemCore()
	if obj == nil || model.MDDObject() != &obj.object {
		panic(fmt.Errorf("mdd: enum type %v has an invalid MDD object", typ))
	}
	obj.object.self, obj.object.id, obj.object.loaded = value, id, true
	obj.object.constant = &Context{constant: true}
	obj.object.item = item
	return enum.CachePredefined(id, model).(interface{ enumItemCore() *EnumItem }).enumItemCore()
}
