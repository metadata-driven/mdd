package meta

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	datetimeLayouts = []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
	}
)

// validate checks relationships after accessors have resolved and cached them.
func (o *node) validate() {
	switch o.section {
	case "models":
		o.validateItems()
	case "attrs":
		o.validateAttribute()
	}
}

func (o *node) validateAttribute() {
	for _, key := range []string{"multiple", "type_kind", "is_collection"} {
		if o.raw.Has(key) {
			panic(fmt.Errorf("%s.%s: cardinality must be expressed in type", o.Path(), key))
		}
	}
	if o.raw.Has("items") && !o.parsedType.isEnum {
		panic(fmt.Errorf("%s.items is allowed only on enum or enum[] declarations", o.Path()))
	}
	typeInfo := o.Type()
	if typeInfo.Collection && typeInfo.Target != nil && typeInfo.Target.Kind() == KindType {
		switch typeInfo.Target.Name() {
		case TypeBool, TypeJSON, TypeFile:
			panic(fmt.Errorf("%s: %s attributes cannot be collections", o.Path(), typeInfo.Target.Name()))
		}
	}
	if o.raw.Has("query") && o.raw["query"] != nil && !typeInfo.Set {
		panic(fmt.Errorf("%s.query requires a set<T> attribute", o.Path()))
	}
	if typeInfo.Target.Kind() == KindModel && !typeInfo.Set && typeInfo.Primitive == nil {
		panic(fmt.Errorf("%s: referenced model %q has no scalar identifier", o.Path(), typeInfo.Target.Path()))
	}
	if o.raw.Has("lookup") && o.raw["lookup"] != nil {
		if !typeInfo.IsReference() || typeInfo.Set {
			panic(fmt.Errorf("%s.lookup requires a model or enum reference", o.Path()))
		}
	}
}

func (o *node) validateItems() {
	attrs := make(map[string]Attribute, len(o.Attrs()))
	for _, attr := range o.Attrs() {
		attrs[attr.Name()] = attr
	}
	id := attrs["id"]
	for _, item := range o.Items() {
		if id != nil {
			validationAttributeValue(id, item.ItemID(), item.Path()+".id")
		} else if item.Raw().Has("id") {
			panic(fmt.Errorf("%s.id: model %q has no identifier", item.Path(), o.Path()))
		}
		data := item.Data()
		for _, key := range data.Keys() {
			attr := attrs[key]
			if attr == nil {
				panic(fmt.Errorf("%s.data.%s: unknown attribute of model %q", item.Path(), key, o.Path()))
			}
			validationAttributeValue(attr, data[key], item.Path()+".data."+key)
		}
	}
}

func validationAttributeValue(attr Attribute, value any, location string) {
	typeInfo := attr.Type()
	if typeInfo.Set {
		panic(fmt.Errorf("%s: a set attribute cannot have a predefined value", location))
	}
	if value == nil {
		return
	}
	if typeInfo.Collection {
		list := reflect.ValueOf(value)
		if list.Kind() != reflect.Slice && list.Kind() != reflect.Array {
			panic(fmt.Errorf("%s must be a list", location))
		}
		for i := range list.Len() {
			validationSingleValue(attr, list.Index(i).Interface(), fmt.Sprintf("%s[%d]", location, i))
		}
		return
	}
	validationSingleValue(attr, value, location)
}

func validationSingleValue(attr Attribute, value any, location string) {
	if value == nil {
		return
	}
	target := attr.Type().Target
	switch target.Kind() {
	case KindEnum:
		name, ok := value.(string)
		if ok {
			for _, item := range target.(Enum).Items() {
				if name == item.Name() || name == item.Path() {
					return
				}
			}
		}
		panic(fmt.Errorf("%s: %v is not an item of enum %q", location, value, target.Path()))
	case KindModel:
		if path, ok := value.(string); ok {
			if item := target.Root().Lookup(path); item != nil && item.Kind() == KindItem {
				if parent := item.Parent(); parent != nil && parent.Path() == target.Path() {
					return
				}
				panic(fmt.Errorf("%s: predefined item %q does not belong to model %q", location, path, target.Path()))
			}
		}
		if target.(Model).SetName() == "" {
			panic(fmt.Errorf("%s: referenced model %q has no identifier", location, target.Path()))
		}
		if !validationInteger(value, false) {
			panic(fmt.Errorf("%s: value %v is incompatible with type %q", location, value, TypeID))
		}
	default:
		validationScalarValue(attr, value, location)
	}
}

func validationScalarValue(attr Attribute, value any, location string) {
	typeInfo := attr.Type()
	target := typeInfo.Primitive
	if target == nil || target.Kind() != KindType {
		panic(fmt.Errorf("%s: value has no scalar storage type", location))
	}
	valid := false
	switch target.Name() {
	case TypeBool:
		_, valid = value.(bool)
	case TypeID:
		valid = validationInteger(value, false)
	case TypeInt:
		valid = validationInteger(value, true)
	case TypeNumber:
		var number string
		number, valid = validationNumber(value)
		precision, scale := typeInfo.Precision, typeInfo.Scale
		if valid && precision > 0 {
			parts := strings.SplitN(strings.TrimPrefix(number, "-"), ".", 2)
			whole := strings.TrimLeft(parts[0], "0")
			fraction := ""
			if len(parts) == 2 {
				fraction = strings.TrimRight(parts[1], "0")
			}
			valid = len(whole) <= precision-scale && len(fraction) <= scale
		}
	case TypeString:
		var str string
		str, valid = value.(string)
		if valid && typeInfo.Length > 0 {
			valid = utf8.RuneCountInString(str) <= typeInfo.Length
		}
	case TypeBinary, TypeFile:
		switch v := value.(type) {
		case string:
			valid = target.Name() != TypeBinary || typeInfo.Length == 0 || len(v) <= typeInfo.Length
		case []byte:
			valid = target.Name() != TypeBinary || typeInfo.Length == 0 || len(v) <= typeInfo.Length
		}
	case TypeJSON:
		valid = validationJSON(value)
	case TypeDatetime:
		if _, valid = value.(time.Time); !valid {
			if str, ok := value.(string); ok {
				for _, layout := range datetimeLayouts {
					if _, err := time.Parse(layout, str); err == nil {
						valid = true
						break
					}
				}
			}
		}
	}
	if !valid {
		panic(fmt.Errorf("%s: value %v is incompatible with type %q", location, value, typeInfo.Expr))
	}
}

func validationJSON(value any) bool {
	if value == nil {
		return true
	}
	if _, valid := validationNumber(value); valid {
		return true
	}
	current := reflect.ValueOf(value)
	switch current.Kind() {
	case reflect.Bool, reflect.String:
		return true
	case reflect.Interface:
		return current.IsNil() || validationJSON(current.Elem().Interface())
	case reflect.Slice, reflect.Array:
		for i := range current.Len() {
			if !validationJSON(current.Index(i).Interface()) {
				return false
			}
		}
		return true
	case reflect.Map:
		if current.Type().Key().Kind() != reflect.String {
			return false
		}
		iterator := current.MapRange()
		for iterator.Next() {
			if !validationJSON(iterator.Value().Interface()) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func validationInteger(value any, signed bool) bool {
	number := reflect.ValueOf(value)
	switch number.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return signed || number.Int() >= 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if signed {
			return number.Uint() <= math.MaxInt64
		}
		return true
	default:
		return false
	}
}

func validationNumber(value any) (string, bool) {
	number := reflect.ValueOf(value)
	switch number.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(number.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(number.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		value := number.Float()
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", false
		}
		return strconv.FormatFloat(value, 'f', -1, number.Type().Bits()), true
	default:
		return "", false
	}
}
