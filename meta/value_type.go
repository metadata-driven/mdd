package meta

import (
	"fmt"
	"strconv"
	"strings"
)

type valueType struct {
	name       string
	collection bool
	isEnum     bool
	isSet      bool
	length     int
	precision  int
	scale      int
}

func parseValueType(spec string) (result valueType) {
	original := spec
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "set<") {
		if !strings.HasSuffix(spec, ">") {
			panic(fmt.Errorf("invalid type %q", original))
		}
		result.name = strings.TrimSpace(spec[4 : len(spec)-1])
		result.isEnum = result.name == "enum"
		if !validTypePath(result.name) || isScalarType(result.name) || result.isEnum {
			panic(fmt.Errorf("invalid set type %q: expected a model or enum path", original))
		}
		result.isSet = true
		return result
	}
	if strings.HasSuffix(spec, "[]") {
		result.collection = true
		spec = strings.TrimSpace(strings.TrimSuffix(spec, "[]"))
	}

	result.name = spec
	if open := strings.IndexByte(spec, '('); open >= 0 {
		if !strings.HasSuffix(spec, ")") {
			panic(fmt.Errorf("invalid type %q", original))
		}
		result.name = strings.TrimSpace(spec[:open])
		args := strings.Split(spec[open+1:len(spec)-1], ",")
		switch result.name {
		case TypeString, TypeBinary:
			if len(args) != 1 {
				panic(fmt.Errorf("invalid type %q: %s requires one length parameter", original, result.name))
			}
			result.length = valueTypeInteger(args[0], original)
		case TypeNumber:
			if len(args) != 2 {
				panic(fmt.Errorf("invalid type %q: number requires precision and scale", original))
			}
			result.precision = valueTypeInteger(args[0], original)
			result.scale = valueTypeInteger(args[1], original)
			if result.precision <= 0 {
				panic(fmt.Errorf("invalid type %q: precision must be greater than zero", original))
			}
			if result.scale > result.precision {
				panic(fmt.Errorf("invalid type %q: scale must not exceed precision", original))
			}
		default:
			panic(fmt.Errorf("invalid type %q: parameters are supported only for string, binary and number", original))
		}
	}
	if !validTypePath(result.name) {
		panic(fmt.Errorf("invalid type %q", original))
	}
	result.isEnum = result.name == "enum"
	return result
}

func (t valueType) expression() string {
	if t.isSet {
		return "set<" + t.name + ">"
	}
	expr := t.name
	switch {
	case (t.name == TypeString || t.name == TypeBinary) && t.length > 0:
		expr += "(" + strconv.Itoa(t.length) + ")"
	case t.name == TypeNumber && t.precision > 0:
		expr += "(" + strconv.Itoa(t.precision) + "," + strconv.Itoa(t.scale) + ")"
	}
	if t.collection {
		expr += "[]"
	}
	return expr
}

func isScalarType(name string) bool {
	switch name {
	case TypeID, TypeBool, TypeInt, TypeNumber, TypeDatetime, TypeString, TypeBinary, TypeFile, TypeJSON:
		return true
	}
	return false
}

func validTypePath(path string) bool {
	for name := range strings.SplitSeq(path, ".") {
		if name == "" || name[0] < 'a' || name[0] > 'z' {
			return false
		}
		for i := 1; i < len(name); i++ {
			c := name[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
				return false
			}
		}
	}
	return true
}

func valueTypeInteger(value, spec string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		panic(fmt.Errorf("invalid parameter %q in type %q", value, spec))
	}
	for i := range len(value) {
		if value[i] < '0' || value[i] > '9' {
			panic(fmt.Errorf("invalid parameter %q in type %q: expected a non-negative integer", value, spec))
		}
	}
	result, err := strconv.Atoi(value)
	if err != nil {
		panic(fmt.Errorf("invalid parameter %q in type %q: %w", value, spec, err))
	}
	return result
}
