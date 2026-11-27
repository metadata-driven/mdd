package mdd

import "github.com/metadata-driven/mdd/meta"

// EnumItem is a shared, immutable item of an enumeration.
type EnumItem interface {
	ID() uint64
	UID() string
	Is(values ...any) bool

	Name() string
	Title() string
	String() string
	Icon() string
	Description() string
	Meta() meta.Item
	Get(name string) any

	objectValue
}

type enumItem object

var _ EnumItem = (*enumItem)(nil)

func (e *enumItem) objectCore() *object   { return (*object)(e) }
func (e *enumItem) ID() uint64            { return e.objectCore().ID() }
func (e *enumItem) UID() string           { return e.objectCore().UID() }
func (e *enumItem) Is(values ...any) bool { return e.objectCore().Is(values...) }

func (e *enumItem) Meta() meta.Item {
	if e == nil {
		return nil
	}
	return e.item
}

func (e *enumItem) Name() string {
	if item := e.Meta(); item != nil {
		return item.Name()
	}
	return ""
}

func (e *enumItem) Title() string {
	if item := e.Meta(); item != nil {
		return item.Title()
	}
	return ""
}

func (e *enumItem) String() string { return e.Title() }

func (e *enumItem) Description() string {
	if item := e.Meta(); item != nil {
		return item.Description()
	}
	return ""
}

func (e *enumItem) Icon() string {
	if item := e.Meta(); item != nil {
		return item.Icon()
	}
	return ""
}

func (e *enumItem) Get(name string) any {
	item := e.Meta()
	if item == nil {
		return nil
	}
	switch name {
	case "id":
		return e.id
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
