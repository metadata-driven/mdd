package meta

import "github.com/goldic/js"

// IndexType describes how an index organizes its attributes.
type IndexType string

const (
	IndexTypeRegular  IndexType = "regular"
	IndexTypeUnique   IndexType = "unique"
	IndexTypeFulltext IndexType = "fulltext"
)

type TypeInfo struct {
	Expr       string
	Primitive  Primitive
	Target     Node
	Collection bool
	Set        bool
	Length     int
	Precision  int
	Scale      int
}

type Primitive interface {
	Node
}

const (
	TypeID       = "id"
	TypeBool     = "bool"
	TypeInt      = "int"
	TypeNumber   = "number"
	TypeDatetime = "datetime"
	TypeString   = "string"
	TypeBinary   = "binary"
	TypeJSON     = "json"
	TypeFile     = "file"
)

var predefinedTypeSpecs = []js.Object{
	{"name": TypeID, "title": "ID"},
	{"name": TypeBool, "title": "Boolean"},
	{"name": TypeInt, "title": "Integer"},
	{"name": TypeNumber, "title": "Number"},
	{"name": TypeDatetime, "title": "Datetime"},
	{"name": TypeString, "title": "String"},
	{"name": TypeBinary, "title": "Binary"},
	{"name": TypeJSON, "title": "JSON"},
	{"name": TypeFile, "title": "File"},
}

func (t TypeInfo) String() string { return t.Expr }

func (t TypeInfo) IsReference() bool {
	return t.Target != nil && (t.Target.Kind() == KindModel || t.Target.Kind() == KindEnum)
}

// IsFile reports whether the logical type is file.
func (t TypeInfo) IsFile() bool {
	return t.Target != nil && t.Target.Kind() == KindType && t.PrimitiveTypeName() == TypeFile
}

// PrimitiveTypeName returns the primitive type name, or an empty string if absent.
func (t TypeInfo) PrimitiveTypeName() string {
	if t.Primitive == nil {
		return ""
	}
	return t.Primitive.Name()
}
