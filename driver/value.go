package driver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"time"
)

// DefaultValue returns the stored value for an unset column.
func DefaultValue(typ DataType) any {
	switch typ {
	case TypeID:
		return uint64(0)
	case TypeBoolean:
		return false
	case TypeInteger:
		return int64(0)
	case TypeDecimal:
		return "0"
	case TypeDatetime:
		return time.Unix(0, 0).UTC()
	case TypeString, TypeJSON:
		return ""
	case TypeBinary, TypeFile:
		return []byte{}
	default:
		panic("driver: invalid column type")
	}
}

// NormalizeValue replaces an absent value with the column's storage default.
func NormalizeValue(typ DataType, value any) (any, error) {
	if isNil(value) {
		return DefaultValue(typ), nil
	}
	if typ == TypeBinary {
		if data, ok := value.([]byte); ok {
			return data, nil
		}
		return nil, fmt.Errorf("driver: expected []byte for binary value, got %T", value)
	}
	if typ == TypeFile {
		return FileBytes(value)
	}
	return value, nil
}

// NormalizeColumnValue normalizes a value and checks column-specific limits.
func NormalizeColumnValue(col *Column, value any) (any, error) {
	value, err := NormalizeValue(col.Type, value)
	if err != nil {
		return nil, err
	}
	if col.Type == TypeBinary {
		if col.Length < 0 {
			return nil, fmt.Errorf("driver: invalid binary length %d", col.Length)
		}
		if data := value.([]byte); col.Length > 0 && len(data) > col.Length {
			return nil, fmt.Errorf("driver: binary value has %d bytes, maximum is %d", len(data), col.Length)
		}
	}
	return value, nil
}

func isNil(value any) bool {
	switch value.(type) {
	case nil:
		return true
	case uint64, int64, bool, string, time.Time, json.Number:
		return false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

// JSONText serializes a JSON field; an absent value is stored as an empty string.
func JSONText(value any) (string, error) {
	value, err := NormalizeValue(TypeJSON, value)
	if err != nil || value == nil {
		return "", err
	}
	if text, ok := value.(string); ok && text == "" {
		return "", nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	s := string(data)
	if s == "null" {
		return "", nil
	}
	return s, nil
}

// Cast converts a value to the representation for typ. If isCollection is
// true, typ describes the element type and the result is a JSON-compatible
// []any. A scalar is treated as a single-element collection. JSON numbers use
// json.Number, datetimes use UTC strings, and binary values use base64 strings.
// Unsupported values cause a panic. File readers remain lazy until written.
func Cast(value any, typ DataType, isCollection bool) any {
	if isCollection {
		return castCollection(value, typ)
	}
	return castScalar(value, typ)
}

func castCollection(value any, typ DataType) any {
	if value == nil {
		return nil
	}
	if typ == TypeFile || typ == TypeJSON {
		panic(fmt.Errorf("driver: data type %d cannot be a collection", typ))
	}
	items := reflect.ValueOf(value)
	if items.Kind() != reflect.Slice && items.Kind() != reflect.Array || typ == TypeBinary && items.Type() == reflect.TypeFor[[]byte]() {
		value = []any{value}
		items = reflect.ValueOf(value)
	}
	result := make([]any, items.Len())
	for i := range items.Len() {
		item := items.Index(i).Interface()
		if typ == TypeBinary {
			switch v := item.(type) {
			case nil:
			case []byte:
				if v != nil {
					item = base64.StdEncoding.EncodeToString(v)
				} else {
					item = nil
				}
			case string:
				item = base64.StdEncoding.EncodeToString([]byte(v))
			default:
				panic(fmt.Errorf("driver: cannot cast %T to binary collection element", item))
			}
		} else if item != nil || typ == TypeID || typ == TypeDatetime {
			item = castScalar(item, typ)
			switch typ {
			case TypeID:
				item = json.Number(strconv.FormatUint(item.(uint64), 10))
			case TypeInteger:
				item = json.Number(strconv.FormatInt(item.(int64), 10))
			case TypeDecimal:
				item = json.Number(item.(string))
			case TypeDatetime:
				date := item.(time.Time)
				if date.Year() < 0 || date.Year() > 9999 {
					panic(fmt.Errorf("driver: timestamp year outside JSON range: %d", date.Year()))
				}
				item = date.Format(time.RFC3339Nano)
			}
		}
		result[i] = item
	}
	return result
}

func castScalar(value any, typ DataType) any {
	if isNil(value) {
		switch typ {
		case TypeBinary, TypeFile, TypeJSON:
			return nil
		case TypeDatetime:
			return time.Unix(0, 0).UTC()
		default:
			return DefaultValue(typ)
		}
	}
	switch typ {
	case TypeID:
		switch v := value.(type) {
		case interface{ ID() uint64 }:
			return v.ID()
		case uint64:
			return v
		case uint:
			return uint64(v)
		case uint32:
			return uint64(v)
		case uint16:
			return uint64(v)
		case uint8:
			return uint64(v)
		case int:
			if v >= 0 {
				return uint64(v)
			}
		case int64:
			if v >= 0 {
				return uint64(v)
			}
		case int32:
			if v >= 0 {
				return uint64(v)
			}
		case int16:
			if v >= 0 {
				return uint64(v)
			}
		case int8:
			if v >= 0 {
				return uint64(v)
			}
		case string:
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				return n
			}
		case json.Number:
			if n, err := strconv.ParseUint(string(v), 10, 64); err == nil {
				return n
			}
		}
	case TypeInteger:
		switch v := value.(type) {
		case int64:
			return v
		case int:
			return int64(v)
		case int32:
			return int64(v)
		case int16:
			return int64(v)
		case int8:
			return int64(v)
		case uint64:
			if v <= math.MaxInt64 {
				return int64(v)
			}
		case uint:
			if uint64(v) <= math.MaxInt64 {
				return int64(v)
			}
		case uint32:
			return int64(v)
		case uint16:
			return int64(v)
		case uint8:
			return int64(v)
		case string:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
		case json.Number:
			if n, err := v.Int64(); err == nil {
				return n
			}
		}
	case TypeBoolean:
		if v, ok := value.(bool); ok {
			return v
		}
	case TypeDecimal:
		switch v := value.(type) {
		case string:
			if _, err := json.Marshal(json.Number(v)); err == nil {
				return v
			}
		case json.Number:
			if _, err := json.Marshal(v); err == nil {
				return string(v)
			}
		case float32:
			if !math.IsInf(float64(v), 0) && !math.IsNaN(float64(v)) {
				return strconv.FormatFloat(float64(v), 'f', -1, 32)
			}
		case float64:
			if !math.IsInf(v, 0) && !math.IsNaN(v) {
				return strconv.FormatFloat(v, 'f', -1, 64)
			}
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
		}
	case TypeDatetime:
		switch v := value.(type) {
		case time.Time:
			return v.UTC().Truncate(time.Microsecond)
		case string:
			if t, err := ParseTime(v); err == nil {
				return t
			}
		}
	case TypeString:
		switch v := value.(type) {
		case string:
			return v
		case []byte:
			return string(v)
		}
	case TypeBinary:
		switch v := value.(type) {
		case []byte:
			if len(v) == 0 {
				return nil
			}
			return v
		case string:
			if v == "" {
				return nil
			}
			return []byte(v)
		}
	case TypeFile:
		switch v := value.(type) {
		case File:
			return v
		case string, []byte, io.Reader, func() (io.ReadCloser, error):
			return NewFile(v)
		}
	case TypeJSON:
		if v, ok := value.(string); ok && v == "" {
			return nil
		}
		data, err := json.Marshal(value)
		if err != nil {
			panic(fmt.Errorf("driver: cannot encode JSON: %w", err))
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var result any
		if err := decoder.Decode(&result); err != nil {
			panic(fmt.Errorf("driver: cannot decode JSON: %w", err))
		}
		return result
	}
	panic(fmt.Errorf("driver: cannot cast %T to data type %d", value, typ))
}

// ParseTime decodes a timestamp from a storage representation.
func ParseTime(value string) (Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC().Truncate(time.Microsecond), nil
		}
	}
	return Time{}, fmt.Errorf("driver: invalid timestamp %q", value)
}
