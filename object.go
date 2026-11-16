package mdd

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/meta"
)

// Object stores the state shared by a model and its context.
type Object struct {
	self     any
	ctx      *Context
	constant *Context
	tab      *metaTable
	item     meta.Item
	id       uint64
	data     driver.DataObject
	changes  driver.DataObject
	loaded   bool
	deleted  bool
	deleting bool
}

// MDDObject returns the embedded object of a model.
func (o *Object) MDDObject() any { return o }

func (o *Object) getSelf() any {
	return o.self
}

func (o *Object) setSelf(self any) {
	o.self = self
}

func (o *Object) initObject(ctx *Context, tab *metaTable, id uint64) {
	o.ctx = ctx
	o.id = id
	o.tab = tab
}

func (o *Object) setAttribute(name string) (*Context, *metaAttribute, any) {
	if o == nil {
		panic("mdd: object is nil")
	}
	if o.constant != nil {
		panic("mdd: a predefined object has no transaction context")
	}
	attr := o.tab.Attrs[name]
	if attr == nil {
		panic(fmt.Errorf("mdd: unknown attribute %s.%s", o.tab.Name, name))
	}
	if !attr.Type().Set {
		panic(fmt.Errorf("mdd: %s is not a set", attr.Path()))
	}
	return o.ctx, attr, o.self
}

func (o *Object) setID(id uint64) {
	o.id = id
	o.loaded = true
	o.deleted = false
}

func (o *Object) clearCache() {
	clear(o.data)
	clear(o.changes)
	o.loaded = false
	o.deleted = false
}

func (o *Object) clearFiles() {
	for name := range o.data {
		if attr := o.tab.Attrs[name]; attr != nil && attr.Type().IsFile() {
			delete(o.data, name)
		}
	}
}

func (o *Object) prepareCreated() {
	if o.tab.created == nil {
		return
	}
	if value, ok := o.changes["created"].(time.Time); ok && !value.IsZero() {
		return
	}
	o.Set("created", time.Now())
}

func (o *Object) onCommit() {
	if o.tab.updated != nil {
		o.Set("updated", time.Now())
	}
	if hook, ok := o.self.(interface{ OnCommit() }); ok {
		hook.OnCommit()
	}
}

// takeChanges merges non-file values into the cache and returns the fields to write.
func (o *Object) takeChanges() driver.DataObject {
	changes := o.changes
	o.changes = nil
	for name, value := range changes {
		if o.tab.Attrs[name].Type().IsFile() {
			continue
		}
		if o.data == nil {
			o.data = make(driver.DataObject, len(changes))
		}
		o.data[name] = value
	}
	return changes
}

// MDDContext returns the context that owns the object.
func (o *Object) MDDContext() any {
	if o == nil || o.ctx == nil {
		return nil
	}
	return o.ctx.getSelf()
}

func (o *Object) ID() uint64 {
	if o == nil {
		return 0
	}
	if o.ctx != nil && o.id == 0 && !o.deleted {
		o.ctx.txGet(o.tab, o)
	}
	return o.id
}

// UID returns the model path and object ID, separated by '#'.
// It assigns an ID to a new object in the same way as ID.
func (o *Object) UID() string {
	if o == nil {
		return ""
	}
	if o.item != nil && o.item.Parent().Kind() == meta.KindEnum {
		return fmt.Sprintf("%s#%d", o.item.Parent().Path(), o.id)
	}
	if o.tab == nil || o.tab.Model == nil {
		return ""
	}
	id := o.ID()
	if id == 0 {
		return ""
	}
	return fmt.Sprintf("%s#%d", o.tab.Model.Path(), id)
}

func (o *Object) objectKey() objectKey {
	if o == nil {
		return objectKey{}
	}
	return objectKey{tab: o.tab, id: o.ID()}
}

// Is reports whether this object matches any supplied ID or object.
func (o *Object) Is(values ...any) bool {
	return o != nil && isValues(o, values)
}

func isValues(obj objectKeyProvider, values []any) bool {
	if len(values) == 0 {
		return false
	}
	objKey := obj.objectKey()
	for _, val := range values {
		if val == nil {
			continue
		}
		if obj == val {
			return true
		}
		if candidate, ok := val.(objectKeyProvider); ok {
			v := reflect.ValueOf(candidate)
			if v.Kind() == reflect.Pointer && v.IsNil() {
				continue
			}
			left, leftOK := obj.(interface{ MDDObject() any })
			right, rightOK := candidate.(interface{ MDDObject() any })
			if leftOK && rightOK {
				a, aOK := left.MDDObject().(*Object)
				b, bOK := right.MDDObject().(*Object)
				if aOK && bOK && a != nil && a == b {
					return true
				}
			}
			if other := candidate.objectKey(); objKey.tab != nil && objKey.id != 0 && objKey == other {
				return true
			}
			continue
		}
		if vID := comparableID(val); vID != 0 {
			if objKey.id == vID {
				return true
			}
		}
	}
	return false
}

func comparableID(value any) uint64 {
	switch n := value.(type) {
	case uint64:
		return n
	case int:
		if n > 0 {
			return uint64(n)
		}
	case int64:
		if n > 0 {
			return uint64(n)
		}
	default:
		v := reflect.ValueOf(value)
		switch v.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if n := v.Int(); n > 0 {
				return uint64(n)
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return v.Uint()
		}
	}
	return 0
}

// String returns the model title evaluated for this object.
func (o *Object) String() string {
	return o.presentation(func(tab *metaTable) Template { return tab.title })
}

// Icon returns the model icon evaluated for this object.
func (o *Object) Icon() string {
	return o.presentation(func(tab *metaTable) Template { return tab.icon })
}

// Description returns the model description evaluated for this object.
func (o *Object) Description() string {
	return o.presentation(func(tab *metaTable) Template { return tab.description })
}

func (o *Object) Model() meta.Model {
	if o == nil || o.tab == nil {
		return nil
	}
	return o.tab.Model
}

// Delete removes this object without changing references to it.
func (o *Object) Delete() {
	if o == nil {
		return
	}
	if o.constant != nil {
		panic("mdd: cannot delete a predefined object")
	}
	o.ctx.checkWritable()
	if o.deleted || o.deleting {
		return
	}
	o.deleting = true
	defer func() { o.deleting = false }()
	hook, onDelete := o.self.(interface{ OnDelete() })
	_, onAfterCommit := o.self.(interface{ OnAfterCommit() })
	exists := o.id != 0
	if exists && (onDelete || onAfterCommit) {
		exists = !o.Deleted()
	}
	o.ctx.txDelete(o, exists)
	o.clearCache()
	o.deleted = true
	if onDelete && exists {
		hook.OnDelete()
	}
}

// Deleted reports whether this object is absent from its primary source.
func (o *Object) Deleted() bool {
	if o == nil {
		return true
	}
	if o.constant != nil {
		return false
	}
	if o.deleted {
		return true
	}
	if o.id == 0 {
		return false
	}
	if !o.loaded {
		row := o.ctx.txGet(o.tab, o)
		if row == nil {
			o.deleted = true
			return true
		}
		o.loaded = true
		o.mergeData(o.tab, row)
	}
	return false
}

func (o *Object) presentation(get func(*metaTable) Template) string {
	if o == nil || o.tab == nil || o.tab.Model == nil {
		return ""
	}
	template := get(o.tab)
	if template == nil {
		return ""
	}
	var result strings.Builder
	check(template.Execute(&result, o.self))
	return result.String()
}

func (o *Object) Get(attr string) any {
	value, _, _ := o.getValue(attr)
	return value
}

func (o *Object) getValue(attr string) (any, *metaAttribute, *Context) {
	if o == nil {
		return nil, nil, nil
	}
	if o.item != nil && o.item.Parent().Kind() == meta.KindEnum {
		return enumItemValue(o.item, o.id, attr), nil, nil
	}
	if attr == "id" {
		return o.ID(), o.tab.Attrs[attr], o.ctx
	}
	metaAttr := o.attribute(attr)
	if o.constant != nil {
		value := o.data[attr]
		if value != nil && metaAttr.Type().IsFile() {
			value = driver.NewFile(value)
		} else {
			value = copyPredefinedValue(value)
		}
		return value, metaAttr, o.constant
	}
	if o.deleted {
		return nil, metaAttr, o.ctx
	}
	if value, ok := o.changes[attr]; ok && !metaAttr.Type().IsFile() {
		return value, metaAttr, o.ctx
	}
	if value, ok := o.data[attr]; ok {
		return value, metaAttr, o.ctx
	}
	if o.id == 0 {
		return nil, metaAttr, o.ctx
	}
	tab := metaAttr.storageTable()
	if tab != o.tab && !o.loaded && o.Deleted() {
		return nil, metaAttr, o.ctx
	}
	row := o.ctx.txGet(tab, o)
	if row != nil {
		if tab == o.tab {
			o.loaded = true
		}
		o.mergeData(tab, row)
		return o.data[attr], metaAttr, o.ctx
	}
	if tab == o.tab {
		o.deleted = true
	}
	return nil, metaAttr, o.ctx
}

func copyPredefinedValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return bytes.Clone(v)
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = copyPredefinedValue(item)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(v))
		for name, item := range v {
			result[name] = copyPredefinedValue(item)
		}
		return result
	default:
		return value
	}
}

func (o *Object) mergeData(tab *metaTable, row driver.DataObject) {
	if o.data == nil {
		o.data = row
		for name := range row {
			if field := o.tab.Attrs[name]; field == nil || field.table != tab {
				o.data = make(driver.DataObject, len(row))
				break
			}
		}
	}
	preserveCached := o.ctx.isAfterCommit()
	for name, item := range row {
		if field := o.tab.Attrs[name]; field != nil && field.table == tab {
			if _, cached := o.data[name]; cached && preserveCached {
				continue
			}
			o.data[name] = item
		}
	}
}

func (o *Object) Set(attr string, v any) {
	if o == nil {
		return
	}
	if o.constant != nil {
		panic("mdd: cannot change a predefined object")
	}
	o.ctx.checkWritable()
	if o.deleted {
		panic(ErrNotFound)
	}
	metaAttr := o.attribute(attr)
	if attr == "id" {
		panic("mdd: id cannot be changed")
	}
	metaAttr.storageTable()
	if metaAttr.column == nil {
		panic(fmt.Errorf("mdd: %s has no storage column", metaAttr.Path()))
	}
	value := o.castValue(metaAttr, v)
	if metaAttr == o.tab.created && o.id == 0 && (v == nil || reflect.ValueOf(v).IsZero()) {
		value = time.Time{}
	}
	o.ctx.txSet(o)
	if o.changes == nil {
		o.changes = make(driver.DataObject)
	}
	o.changes[attr] = value
	delete(o.data, attr)
}

func (o *Object) castValue(attr *metaAttribute, value any) any {
	info := attr.Type()
	if info.Collection && value != nil {
		binary := info.Target.Kind() == meta.KindType && info.PrimitiveTypeName() == meta.TypeBinary
		items := reflect.ValueOf(value)
		if items.Kind() != reflect.Slice && items.Kind() != reflect.Array || binary && items.Type() == reflect.TypeFor[[]byte]() {
			value = []any{value}
			items = reflect.ValueOf(value)
		}
		values := make([]any, items.Len())
		for i := range items.Len() {
			values[i] = items.Index(i).Interface()
			if info.IsReference() {
				values[i] = driver.Cast(driver.TypeID, values[i])
			} else if binary && values[i] != nil {
				switch v := values[i].(type) {
				case []byte:
					values[i] = v
				case string:
					values[i] = []byte(v)
				default:
					panic(fmt.Errorf("mdd: %s[%d] requires binary data, got %T", attr.Path(), i, v))
				}
				if data := values[i].([]byte); info.Length > 0 && len(data) > info.Length {
					panic(fmt.Errorf("mdd: %s[%d] has %d bytes, maximum is %d", attr.Path(), i, len(data), info.Length))
				}
			} else if info.PrimitiveTypeName() == meta.TypeDatetime {
				values[i] = driver.Cast(driver.TypeDatetime, values[i])
			}
		}
		value = values
	}
	return driver.Cast(attr.column.Type, value)
}

func (o *Object) attribute(name string) *metaAttribute {
	attr := o.tab.Attrs[name]
	if attr == nil {
		panic(fmt.Errorf("mdd: unknown attribute %s.%s", o.tab.Name, name))
	}
	if attr.Type().Set {
		panic(fmt.Errorf("mdd: %s is a set, not a stored attribute", attr.Path()))
	}
	return attr
}

func predefinedObject(s *schema, tab *metaTable, item meta.Item) *Object {
	id := storedScalar[uint64](item.ItemID(), item.Path())
	if value := tab.Model.Predefined(id); value != nil {
		return value.MDDObject().(*Object)
	}
	obj := must(newModelObject(tab.Model.ModelType()))
	obj.tab, obj.item, obj.id, obj.loaded = tab, item, id, true
	obj.constant = &Context{schema: s, constant: true}
	obj.data = make(driver.DataObject)
	for name, attr := range tab.Attrs {
		if attr.column != nil {
			obj.data[name] = driver.Cast(attr.column.Type, nil)
		}
	}
	for _, row := range s.items {
		if row.path != item.Path() {
			continue
		}
		for name, value := range row.values {
			attr := tab.Attrs[name]
			if attr.Type().IsFile() {
				obj.data[name] = value
			} else {
				obj.data[name] = driver.Cast(attr.column.Type, value)
			}
		}
	}
	model := obj.self.(meta.ModelObject)
	return tab.Model.CachePredefined(id, model).MDDObject().(*Object)
}
