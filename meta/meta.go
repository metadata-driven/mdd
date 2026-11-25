package meta

import (
	"reflect"

	"github.com/goldic/js"
)

// Node kinds describe normalized metadata roles; they are not YAML fields.
const (
	KindUnknown = iota
	KindConfig
	KindDomain
	KindSource
	KindType
	KindModel
	KindAttr
	KindSet
	KindEnum
	KindItem
	KindIndex
	KindGroup
)

// Node contains the properties shared by every metadata node.
type Node interface {
	String() string
	Kind() int
	Path() string
	Parent() Node
	Root() Config
	Raw() js.Object
	Lookup(path string) Node
	Children() []Node
	Name() string
	Title() string
	Description() string
	Icon() string
	// Options returns arbitrary schema and driver settings.
	Options() js.Object
}

type Config interface {
	Node

	// Version returns the document's format version or the parser's current version.
	Version() string

	Types() []Primitive
	AllTypes() []Node
	Domains() []Domain
	Models() []Model
	Sources() []Source

	ModelByName(name string) Model
	// RegisterEnum must finish before concurrent registration or EnumByType calls.
	RegisterEnum(path string, typ reflect.Type) error
	EnumByType(typ reflect.Type) Enum
}

type Domain interface {
	Node
}

type Model interface {
	Node

	// SetName is the canonical name of the model's queryable set, if any.
	SetName() string
	ModelType() reflect.Type

	// ToTitle, ToDescription, and ToIcon return the instance presentation expressions.
	ToTitle() string
	ToDescription() string
	ToIcon() string

	// Register must not run concurrently with Register or ModelType on this model.
	Register(ModelObject) struct{}
	Domains() []Domain
	Attrs() []Attribute
	Groups() []Group
	Items() []Item
	Indexes() []Index
	Source() Source
}

// ModelObject identifies a Go model that exposes its MDD object.
type ModelObject interface {
	MDDObject() any
}

type Source interface {
	Node
	Conn() string
}

type Attribute interface {
	Node
	Type() TypeInfo
	// Domains returns the attribute's effective domains in declaration order.
	Domains() []Domain
	// IndexType returns the declared shorthand index type, or an empty value.
	IndexType() IndexType
	Query() string
	LookupQuery() string
	Source() Source
	Model() string
}

// Group describes a visual grouping of model attributes.
type Group interface {
	Node
	Attrs() []Attribute
}

type Enum interface {
	Attribute
	EnumType() reflect.Type
	Items() []Item
}

type Set interface {
	Attribute
}

type Item interface {
	Node
	// ItemID returns the effective identifier, whether explicit or positional.
	ItemID() any
	// Data returns the item payload.
	Data() js.Object
	Source() Source
	Model() string
}

type Index interface {
	Node
	Attrs() []Attribute
	IndexType() IndexType
	Source() Source
}
