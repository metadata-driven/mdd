package mdd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"iter"
	"log"
	"reflect"
	"strconv"
	"time"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/meta"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func must[T any](value T, err error) T {
	check(err)
	return value
}

// copyPtr returns a pointer to a shallow copy of value, or nil if value is nil.
func copyPtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func safeCall(fn func()) (err error) {
	defer func() {
		err = toError(recover())
	}()
	fn()
	return
}

func toError(value any) error {
	if value == nil {
		return nil
	}
	if e, ok := value.(error); ok {
		return e
	}
	return fmt.Errorf("%v", value)
}

func mapSeq[T1, T2 any](seq iter.Seq[T1], fn func(T1) T2) iter.Seq[T2] {
	return func(yield func(T2) bool) {
		for v := range seq {
			if !yield(fn(v)) {
				return
			}
		}
	}
}

type logWriter struct {
	logger *log.Logger
}

func (w logWriter) Write(p []byte) (int, error) {
	if err := w.logger.Output(4, string(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func withLogPrefix(logger *log.Logger, prefix string) *log.Logger {
	if prefix == "" {
		return logger
	}
	return log.New(logWriter{logger: logger}, "["+prefix+"] ", 0)
}

func convertPrimitive[T any](value any, name string) (result T, ok bool) {
	if result, ok = value.(T); ok {
		return
	}
	switch any(result).(type) {
	case uint64:
		return any(storedScalar[uint64](value, name)).(T), true
	case int64:
		return any(storedScalar[int64](value, name)).(T), true
	case int:
		return any(int(primitiveInt(value, strconv.IntSize, name))).(T), true
	case int8:
		return any(int8(primitiveInt(value, 8, name))).(T), true
	case int16:
		return any(int16(primitiveInt(value, 16, name))).(T), true
	case int32:
		return any(int32(primitiveInt(value, 32, name))).(T), true
	case uint:
		return any(uint(primitiveUint(value, strconv.IntSize, name))).(T), true
	case uint8:
		return any(uint8(primitiveUint(value, 8, name))).(T), true
	case uint16:
		return any(uint16(primitiveUint(value, 16, name))).(T), true
	case uint32:
		return any(uint32(primitiveUint(value, 32, name))).(T), true
	case float64:
		return any(storedScalar[float64](value, name)).(T), true
	case float32:
		text := primitiveNumber(value)
		number, err := strconv.ParseFloat(text, 32)
		if err != nil {
			panic(fmt.Errorf("mdd: cannot read %s value %T as float32", name, value))
		}
		return any(float32(number)).(T), true
	case bool:
		return any(storedScalar[bool](value, name)).(T), true
	case string:
		return any(storedScalar[string](value, name)).(T), true
	case time.Time:
		return any(storedScalar[time.Time](value, name)).(T), true
	default:
		return result, false
	}
}

func primitiveNumber(value any) string {
	switch v := value.(type) {
	case int:
		return strconv.FormatInt(int64(v), 10)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return string(v)
	case string:
		return v
	default:
		return ""
	}
}

func primitiveInt(value any, bits int, name string) int64 {
	number, err := strconv.ParseInt(primitiveNumber(value), 10, bits)
	if err != nil {
		panic(fmt.Errorf("mdd: cannot read %s value %T as int%d", name, value, bits))
	}
	return number
}

func primitiveUint(value any, bits int, name string) uint64 {
	number, err := strconv.ParseUint(primitiveNumber(value), 10, bits)
	if err != nil {
		panic(fmt.Errorf("mdd: cannot read %s value %T as uint%d", name, value, bits))
	}
	return number
}

func storedScalar[T any](value any, name string) T {
	if result, ok := value.(T); ok {
		return result
	}
	var zero T
	switch any(zero).(type) {
	case uint64:
		var text string
		switch v := value.(type) {
		case int64:
			if v >= 0 {
				return any(uint64(v)).(T)
			}
		case int:
			if v >= 0 {
				return any(uint64(v)).(T)
			}
		case float64:
			text = strconv.FormatFloat(v, 'f', -1, 64)
		case json.Number:
			text = string(v)
		case string:
			text = v
		}
		if number, err := strconv.ParseUint(text, 10, 64); err == nil {
			return any(number).(T)
		}
	case int64:
		var text string
		switch v := value.(type) {
		case int:
			return any(int64(v)).(T)
		case float64:
			text = strconv.FormatFloat(v, 'f', -1, 64)
		case json.Number:
			text = string(v)
		case string:
			text = v
		}
		if number, err := strconv.ParseInt(text, 10, 64); err == nil {
			return any(number).(T)
		}
	case float64:
		var text string
		switch v := value.(type) {
		case int64:
			return any(float64(v)).(T)
		case int:
			return any(float64(v)).(T)
		case json.Number:
			text = string(v)
		case string:
			text = v
		}
		if number, err := strconv.ParseFloat(text, 64); err == nil {
			return any(number).(T)
		}
	case time.Time:
		if text, ok := value.(string); ok {
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
				if parsed, err := time.Parse(layout, text); err == nil {
					return any(parsed).(T)
				}
			}
		}
	case string:
		if data, ok := value.([]byte); ok {
			return any(string(data)).(T)
		}
	case bool:
		if text, ok := value.(string); ok {
			if parsed, err := strconv.ParseBool(text); err == nil {
				return any(parsed).(T)
			}
		}
	}
	return convertValue(value, reflect.TypeFor[T](), name).Interface().(T)
}

func convertAttribute(value any, attr *metaAttribute, ctx *Context, target reflect.Type, name string) reflect.Value {
	if attr == nil || attr.Type().Target == nil {
		return convertValue(value, target, name)
	}
	info := attr.Type()
	element := target
	if info.Collection {
		if target.Kind() != reflect.Slice {
			return convertValue(value, target, attr.Path())
		}
		element = target.Elem()
		if info.Target.Kind() == meta.KindType && info.PrimitiveTypeName() == meta.TypeBinary && isBinaryType(element) {
			list := reflect.ValueOf(value)
			if list.Kind() != reflect.Slice && list.Kind() != reflect.Array {
				panic(fmt.Errorf("mdd: %s is not a list", attr.Path()))
			}
			if list.Kind() == reflect.Slice && list.IsNil() {
				return reflect.Zero(target)
			}
			result := reflect.MakeSlice(target, list.Len(), list.Len())
			for i := range list.Len() {
				item := list.Index(i).Interface()
				if item == nil {
					continue
				}
				encoded, ok := item.(string)
				if !ok {
					panic(fmt.Errorf("mdd: %s[%d] contains invalid binary value %T", attr.Path(), i, item))
				}
				data, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					panic(fmt.Errorf("mdd: %s[%d] contains invalid base64: %w", attr.Path(), i, err))
				}
				result.Index(i).Set(convertBinary(data, element, attr.Path()))
			}
			return result
		}
	}
	dynamicObject := element == reflect.TypeFor[Object]()
	dynamicEnumItem := element == reflect.TypeFor[EnumItem]()
	if !info.IsReference() || (element.Kind() != reflect.Pointer && !dynamicObject && !dynamicEnumItem) {
		return convertValue(value, target, attr.Path())
	}
	var resolve func(any, string) reflect.Value
	switch info.Target.Kind() {
	case meta.KindModel:
		if dynamicEnumItem {
			panic(fmt.Errorf("mdd: %s references a model; use mdd.Object", attr.Path()))
		}
		var tab *metaTable
		if dynamicObject || ctx.constant {
			tab = ctx.schema.tables[info.Target.Path()]
			if tab == nil || (!dynamicObject && tab.Model.ModelType() != element.Elem()) {
				panic(fmt.Errorf("mdd: %s references a different model type", attr.Path()))
			}
		} else {
			tab = ctx.schema.metaTableByType(element.Elem())
		}
		if tab.Model.Path() != info.Target.Path() {
			panic(fmt.Errorf("mdd: %s references model %q, not %q", attr.Path(), info.Target.Path(), tab.Model.Path()))
		}
		resolve = func(item any, path string) reflect.Value {
			if item == nil {
				return reflect.Zero(element)
			}
			id := storedScalar[uint64](item, path)
			if id == 0 {
				return reflect.Zero(element)
			}
			return reflect.ValueOf(ctx.newObject(tab, id).self)
		}
	case meta.KindEnum:
		enum := info.Target.(meta.Enum)
		if dynamicObject {
			panic(fmt.Errorf("mdd: %s references an enum; use mdd.EnumItem", attr.Path()))
		}
		if !dynamicEnumItem {
			registered := ctx.schema.config.EnumByType(element.Elem())
			if registered == nil || registered.Path() != enum.Path() {
				panic(fmt.Errorf("mdd: enum type %v does not match %s", element, attr.Path()))
			}
		}
		resolve = func(item any, path string) reflect.Value {
			if item == nil {
				return reflect.Zero(element)
			}
			id := storedScalar[uint64](item, path)
			if id == 0 {
				return reflect.Zero(element)
			}
			return reflect.ValueOf(ctx.enumItem(enum, id))
		}
	}
	if !info.Collection {
		return resolve(value, attr.Path())
	}
	list := reflect.ValueOf(value)
	if list.Kind() != reflect.Slice && list.Kind() != reflect.Array {
		panic(fmt.Errorf("mdd: %s is not a list", attr.Path()))
	}
	if list.Kind() == reflect.Slice && list.IsNil() {
		return reflect.Zero(target)
	}
	result := reflect.MakeSlice(target, list.Len(), list.Len())
	for i := range list.Len() {
		result.Index(i).Set(resolve(list.Index(i).Interface(), fmt.Sprintf("%s[%d]", attr.Path(), i)))
	}
	return result
}

// convertValue converts driver values to the requested scalar or slice type.
func convertValue(value any, target reflect.Type, name string) reflect.Value {
	if value == nil {
		if target.Kind() == reflect.Interface || target.Kind() == reflect.Slice || target.Kind() == reflect.Map || target.Kind() == reflect.Pointer {
			return reflect.Zero(target)
		}
		panic(fmt.Errorf("mdd: %s is null", name))
	}
	original := reflect.ValueOf(value)
	if original.Type().AssignableTo(target) {
		return original
	}
	if _, ok := value.(driver.File); ok && isBinaryType(target) {
		return convertBinary(value, target, name)
	}
	if item, ok := value.(interface{ ID() uint64 }); ok {
		switch target.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if original.Kind() == reflect.Pointer && original.IsNil() {
				return reflect.Zero(target)
			}
			value = item.ID()
			original = reflect.ValueOf(value)
		}
	}
	if target.Kind() == reflect.Slice && (original.Kind() == reflect.Slice || original.Kind() == reflect.Array) {
		if original.Kind() == reflect.Slice && original.IsNil() {
			return reflect.Zero(target)
		}
		result := reflect.MakeSlice(target, original.Len(), original.Len())
		for i := range original.Len() {
			result.Index(i).Set(convertValue(original.Index(i).Interface(), target.Elem(), fmt.Sprintf("%s[%d]", name, i)))
		}
		return result
	}
	if target == reflect.TypeFor[time.Time]() {
		if text, ok := value.(string); ok {
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
				if parsed, err := time.Parse(layout, text); err == nil {
					return reflect.ValueOf(parsed)
				}
			}
		}
	}
	result := reflect.New(target).Elem()
	switch target.Kind() {
	case reflect.Bool:
		if n, err := strconv.ParseBool(fmt.Sprint(value)); err == nil {
			result.SetBool(n)
			return result
		}
	case reflect.String:
		switch v := value.(type) {
		case string:
			result.SetString(v)
			return result
		case []byte:
			result.SetString(string(v))
			return result
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(fmt.Sprint(value), 10, target.Bits())
		if err == nil {
			result.SetInt(n)
			return result
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(fmt.Sprint(value), 10, target.Bits())
		if err == nil {
			result.SetUint(n)
			return result
		}
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(fmt.Sprint(value), target.Bits())
		if err == nil {
			result.SetFloat(n)
			return result
		}
	}
	panic(fmt.Errorf("mdd: cannot read %s value %T as %v", name, value, target))
}

func isBinaryType(target reflect.Type) bool {
	return target.Kind() == reflect.Slice && target.Elem().Kind() == reflect.Uint8
}

// convertBinary converts a binary value to the requested byte-slice type.
func convertBinary(value any, target reflect.Type, name string) reflect.Value {
	if value == nil {
		return reflect.Zero(target)
	}
	var data []byte
	switch v := value.(type) {
	case driver.File:
		data = v.Bytes()
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		raw := reflect.ValueOf(value)
		if raw.Kind() != reflect.Slice || raw.Type().Elem().Kind() != reflect.Uint8 {
			panic(fmt.Errorf("mdd: %s contains invalid binary value %T", name, value))
		}
		if !raw.IsNil() {
			data = make([]byte, raw.Len())
			for i := range raw.Len() {
				data[i] = byte(raw.Index(i).Uint())
			}
		}
	}
	if data == nil {
		return reflect.Zero(target)
	}
	result := reflect.MakeSlice(target, len(data), len(data))
	for i, b := range data {
		result.Index(i).SetUint(uint64(b))
	}
	return result
}

type objectFactory struct {
	typ         reflect.Type
	objectField []int
	enumItem    bool
}

func newObjectFactory(typ reflect.Type, isEnum bool) (objectFactory, error) {
	if typ.Kind() != reflect.Struct {
		return objectFactory{}, fmt.Errorf("mdd: invalid model type %v", typ)
	}
	fieldType := reflect.TypeFor[Object]()
	if isEnum {
		fieldType = reflect.TypeFor[EnumItem]()
	}
	field, ok := typ.FieldByName(fieldType.Name())
	if !ok || !field.Anonymous || field.Type != fieldType {
		return objectFactory{}, fmt.Errorf("mdd: type %v must embed %v", typ, fieldType)
	}
	model := reflect.New(typ)
	embedded, err := model.Elem().FieldByIndexErr(field.Index)
	if err != nil || !embedded.CanSet() {
		return objectFactory{}, fmt.Errorf("mdd: cannot initialize the embedded %v of type %v", fieldType, typ)
	}
	if !model.Type().Implements(fieldType) {
		return objectFactory{}, fmt.Errorf("mdd: type %v must implement %v", model.Type(), fieldType)
	}
	factory := objectFactory{typ: typ, objectField: field.Index, enumItem: isEnum}
	obj := new(object)
	factory.bind(model, embedded, obj)
	if obj.self.objectCore() != obj {
		return objectFactory{}, fmt.Errorf("mdd: type %v must expose its initialized %v", typ, fieldType)
	}
	return factory, nil
}

func (f objectFactory) newObject() *object {
	obj := new(object)
	if f.typ == nil {
		obj.self = obj
		return obj
	}
	model := reflect.New(f.typ)
	f.bind(model, model.Elem().FieldByIndex(f.objectField), obj)
	return obj
}

func (f objectFactory) bind(model, embedded reflect.Value, obj *object) {
	if f.enumItem {
		embedded.Set(reflect.ValueOf((*enumItem)(obj)))
	} else {
		embedded.Set(reflect.ValueOf(obj))
	}
	obj.self = model.Interface().(objectValue)
}
