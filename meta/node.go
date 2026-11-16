package meta

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/goldic/js"
)

type node struct {
	raw         js.Object
	section     string
	parent      *node
	root        *node
	kind        int
	name        string
	path        string
	title       string
	description string
	icon        string
	options     js.Object
	itemID      any
	itemData    js.Object
	conn        string
	lookupQuery string
	model       string
	modelType   reflect.Type
	enumType    reflect.Type
	predefined  *sync.Map
	set         string
	parsedType  valueType
	indexType   IndexType
	groupAttrs  []Attribute
	cache       map[int]any
	enumTypes   map[reflect.Type]Enum
}

var (
	_ Node      = (*node)(nil)
	_ Config    = (*node)(nil)
	_ Domain    = (*node)(nil)
	_ Primitive = (*node)(nil)
	_ Model     = (*node)(nil)
	_ Source    = (*node)(nil)
	_ Attribute = (*node)(nil)
	_ Group     = (*node)(nil)
	_ Enum      = (*node)(nil)
	_ Set       = (*node)(nil)
	_ Item      = (*node)(nil)
	_ Index     = (*node)(nil)
)

const (
	cacheChildren = iota
	cacheTypes
	cacheAllTypes
	cacheDomains
	cacheModels
	cacheSources
	cacheAttrs
	cacheItems
	cacheSource
	cacheType
	cacheQuery
	cacheCache
	cacheCacheTTL
	cacheID
	cacheIndexes
	cacheStore
	cacheMemorySource
	cacheAttributeLayout
)

type attributeLayout struct {
	attrs  []Attribute
	groups []Group
}

func (o *node) Raw() js.Object      { return o.raw }
func (o *node) Options() js.Object  { return o.options }
func (o *node) Data() js.Object     { return o.itemData }
func (o *node) Root() Config        { return o.root }
func (o *node) String() string      { return o.path }
func (o *node) Kind() int           { return o.kind }
func (o *node) Name() string        { return o.name }
func (o *node) Path() string        { return o.path }
func (o *node) Title() string       { return o.title }
func (o *node) Icon() string        { return o.icon }
func (o *node) Conn() string        { return o.conn }
func (o *node) LookupQuery() string { return o.lookupQuery }
func (o *node) Model() string       { return o.model }

func (o *node) ItemID() any { return o.itemID }

func (o *node) IndexType() IndexType { return o.indexType }

func (o *node) Description() string {
	return o.description
}

func (o *node) Parent() Node {
	if o.parent == nil {
		return nil
	}
	return o.parent
}

func newRootNode(raw js.Object) *node {
	result := &node{raw: raw, section: "root", enumTypes: make(map[reflect.Type]Enum)}
	result.root = result
	result.initialize()
	result.warm()
	return result
}

func (o *node) newChild(raw js.Object, section string) *node {
	result := &node{raw: raw, section: section, parent: o, root: o.root}
	result.initialize()
	return result
}

func (o *node) initialize() {
	metadata := o.metadata()
	o.name = optionalString(metadata, "name", o.section+".name")
	if o.section == "groups" {
		o.name = optionalString(metadata, "group", o.section+".group")
	}
	if o.name == "" {
		switch o.section {
		case "root":
			o.name = "project"
		case "indexes":
			o.name = strings.Join(stringList(o.raw["attrs"], o.parent.path+".indexes.attrs"), "_")
			if o.name != "" && (o.raw["type"] == string(IndexTypeFulltext) || o.raw["type"] == string(IndexTypeUnique)) {
				o.name += "_" + o.raw["type"].(string)
			}
		}
	}

	switch o.section {
	case "root":
		o.path = "$"
	case "models":
		o.path = o.name
	case "attrs", "items":
		o.path = o.parent.path + "." + o.name
	case "types", "domains", "sources":
		o.path = o.section + "." + o.name
	default:
		o.path = o.parent.path + "." + o.section + "." + o.name
	}
	o.options = optionalMap(metadata, "options", o.path+".options")

	if o.section == "attrs" {
		o.parsedType = valueType{name: TypeString}
		if o.raw.Has("type") && !o.raw.Get("type").IsNull() {
			o.parsedType = parseValueType(optionalString(o.raw, "type", o.path+".type"))
		}
	}

	switch o.section {
	case "root":
		o.kind = KindConfig
	case "types":
		o.kind = KindType
	case "domains":
		o.kind = KindDomain
	case "sources":
		o.kind = KindSource
	case "models":
		o.kind = KindModel
		o.predefined = &sync.Map{}
	case "attrs":
		switch {
		case o.parsedType.isEnum:
			o.kind = KindEnum
			o.predefined = &sync.Map{}
		case o.parsedType.isSet:
			o.kind = KindSet
		default:
			o.kind = KindAttr
		}
	case "items":
		o.kind = KindItem
	case "indexes":
		o.kind = KindIndex
	case "groups":
		o.kind = KindGroup
	default:
		o.kind = KindUnknown
	}

	if o.section == "items" {
		data := optionalMap(o.raw, "data", o.path+".data")
		if data.Has("id") {
			panic(fmt.Errorf("%s.data.id is unsupported (move id next to name)", o.path))
		}
		o.itemData = data.Clone()
	}
	o.title = or(o.text("title"), o.name)
	o.description = o.text("description")
	o.icon = o.text("icon")

	switch o.section {
	case "sources":
		o.conn = o.text("conn")
	case "models":
		o.set = o.text("set")
		if o.set != "" && (!validTypePath(o.set) || strings.Contains(o.set, ".")) {
			panic(fmt.Errorf("%s.set must be a local identifier", o.Path()))
		}
	case "attrs":
		o.lookupQuery = o.text("lookup")
		if o.raw.Has("indexed") {
			panic(fmt.Errorf("%s.indexed is unsupported (use index: regular or remove the field)", o.path))
		}
		if value := o.raw["index"]; value != nil {
			o.indexType = parseIndexType(value, o.path+".index")
		}
	case "indexes":
		o.indexType = IndexTypeRegular
		if value := o.raw["type"]; value != nil {
			o.indexType = parseIndexType(value, o.path+".type")
		}
	}

	if o.section == "models" {
		o.model = o.name
	} else if o.parent != nil {
		o.model = o.parent.model
	}
}

func (o *node) metadata() js.Object {
	if o.section == "root" {
		return optionalMap(o.raw, "project", "project")
	}
	return o.raw
}

func (o *node) text(key string) string {
	return optionalString(o.metadata(), key, o.Path()+"."+key)
}

func (o *node) Lookup(path string) Node {
	store := cached(o.root, cacheStore, func() map[string]Node {
		result := make(map[string]Node)
		var visit func(Node)
		visit = func(node Node) {
			if previous := result[node.Path()]; previous != nil {
				panic(fmt.Errorf("duplicate metadata path %q", node.Path()))
			}
			result[node.Path()] = node
			for _, child := range node.Children() {
				visit(child)
			}
		}
		visit(o.root)
		for _, model := range o.root.Models() {
			name := model.SetName()
			if name == "" {
				continue
			}
			if previous := result[name]; previous != nil && previous != model {
				if previous.Kind() == KindModel && previous.(Model).SetName() == name {
					panic(fmt.Errorf("duplicate set name %q", name))
				}
				panic(fmt.Errorf("set name %q conflicts with metadata path %q", name, previous.Path()))
			}
			result[name] = model
		}
		return result
	})
	return store[path]
}

func validateName(name, location string) {
	if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, ".[]<> \t\r\n") {
		panic(fmt.Errorf("%s.name must be a nonempty local identifier", location))
	}
}

func declarations[T Node](o *node, section string) []T {
	list := objectList(o.raw, section, o.Path()+"."+section)
	result := make([]T, 0, len(list))
	names := make(map[string]bool, len(list))
	for i, raw := range list {
		child := o.newChild(raw, section)
		name := child.Name()
		validateName(name, fmt.Sprintf("%s.%s[%d]", o.Path(), section, i))
		if names[name] {
			panic(fmt.Errorf("duplicate %s name %q in %s", section, name, o.Path()))
		}
		names[name] = true
		result = append(result, any(child).(T))
	}
	return result
}

func appendNodes[T Node](result []Node, values []T) []Node {
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func (o *node) Children() []Node {
	return cached(o, cacheChildren, func() []Node {
		var result []Node
		switch o.section {
		case "root":
			result = appendNodes(result, o.Types())
			result = appendNodes(result, o.Domains())
			result = appendNodes(result, o.Models())
			result = appendNodes(result, o.Sources())
		case "models":
			result = appendNodes(result, o.Attrs())
			result = appendNodes(result, o.Groups())
			result = appendNodes(result, o.Indexes())
			result = appendNodes(result, o.Items())
		case "attrs":
			if o.Kind() == KindEnum {
				result = appendNodes(result, o.Items())
			}
		}
		return result
	})
}

func (o *node) Types() []Primitive {
	return cached(o, cacheTypes, func() []Primitive {
		if o.section != "root" {
			return nil
		}
		result := make([]Primitive, len(predefinedTypeSpecs))
		for i, spec := range predefinedTypeSpecs {
			result[i] = o.newChild(spec.Clone(), "types")
		}
		return result
	})
}

func (o *node) AllTypes() []Node {
	return cached(o, cacheAllTypes, func() []Node {
		if o.section != "root" {
			return nil
		}
		result := make([]Node, 0, len(o.Types())+len(o.Models()))
		result = appendNodes(result, o.Types())
		result = appendNodes(result, o.Models())
		for _, model := range o.Models() {
			for _, attr := range model.Attrs() {
				if attr.Kind() == KindEnum {
					result = append(result, attr)
				}
			}
		}
		return result
	})
}

func (o *node) Models() []Model {
	return cached(o, cacheModels, func() []Model {
		if o.section != "root" {
			return nil
		}
		return declarations[Model](o, "models")
	})
}

func (o *node) Domains() []Domain {
	return cached(o, cacheDomains, func() []Domain {
		switch o.section {
		case "root":
			return declarations[Domain](o, "domains")
		case "attrs":
			if !o.raw.Has("domains") {
				return o.parent.Domains()
			}
		case "models":
		default:
			return nil
		}
		value := o.raw["domains"]
		if name, ok := value.(string); ok {
			value = []any{name}
		}
		if _, ok := value.([]any); !ok && (value != nil || o.section == "attrs") {
			panic(fmt.Errorf("%s.domains must be a string or a list of strings", o.Path()))
		}
		names := stringList(value, o.Path()+".domains")
		result := make([]Domain, 0, len(names))
		seen := make(map[string]bool, len(names))
		for _, name := range names {
			domain := findByName(o.root.Domains(), name)
			if domain == nil || seen[name] {
				panic(fmt.Errorf("%s references unknown or repeated domain %q", o.Path(), name))
			}
			seen[name] = true
			result = append(result, domain)
		}
		return result
	})
}

func (o *node) Sources() []Source {
	return cached(o, cacheSources, func() []Source {
		if o.section != "root" {
			return nil
		}
		result := declarations[Source](o, "sources")
		for _, model := range o.Models() {
			for _, attr := range model.Attrs() {
				if attr.Kind() != KindEnum {
					continue
				}
				for _, source := range result {
					if source.Conn() == "mem:" {
						return result
					}
				}
				return append(result, o.memorySource())
			}
		}
		return result
	})
}

func (o *node) memorySource() Source {
	return cached(o.root, cacheMemorySource, func() Source {
		names := make(map[string]bool)
		for _, source := range objectList(o.root.raw, "sources", "sources") {
			names[source.GetStr("name")] = true
		}
		name := "mem"
		for names[name] {
			name = "enum_" + name
		}
		return o.root.newChild(js.Object{"name": name, "conn": "mem:"}, "sources")
	})
}

func (o *node) Attrs() []Attribute {
	if o.section == "models" {
		return cached(o, cacheAttrs, func() []Attribute { return o.attributeLayout().attrs })
	}
	if o.section == "groups" {
		return o.groupAttrs
	}
	return cached(o, cacheAttrs, func() []Attribute {
		switch o.section {
		case "indexes":
			names := stringList(o.raw["attrs"], o.Path()+".attrs")
			if len(names) == 0 {
				panic(fmt.Errorf("%s.attrs must not be empty", o.Path()))
			}
			result := make([]Attribute, 0, len(names))
			seen := make(map[string]bool, len(names))
			for _, name := range names {
				attr := findByName(o.parent.Attrs(), name)
				if attr == nil || attr.Type().Set || seen[name] {
					panic(fmt.Errorf("%s references unknown, repeated or queryable attribute %q", o.Path(), name))
				}
				seen[name] = true
				result = append(result, attr)
			}
			return result
		default:
			return nil
		}
	})
}

func (o *node) Groups() []Group {
	if o.section != "models" {
		return nil
	}
	return o.attributeLayout().groups
}

func (o *node) attributeLayout() attributeLayout {
	return cached(o, cacheAttributeLayout, func() attributeLayout {
		var result attributeLayout
		attrs := make(map[string]bool)
		groups := make(map[string]bool)
		addAttr := func(raw js.Object, location string) Attribute {
			attr := o.newChild(raw, "attrs")
			validateName(attr.Name(), location)
			if attrs[attr.Name()] {
				panic(fmt.Errorf("duplicate attrs name %q in %s", attr.Name(), o.Path()))
			}
			attrs[attr.Name()] = true
			result.attrs = append(result.attrs, attr)
			return attr
		}
		for i, raw := range objectList(o.raw, "attrs", o.Path()+".attrs") {
			location := fmt.Sprintf("%s.attrs[%d]", o.Path(), i)
			if !raw.Has("group") || raw.Has("name") {
				addAttr(raw, location)
				continue
			}
			group := o.newChild(raw, "groups")
			validateName(group.Name(), location+".group")
			if groups[group.Name()] {
				panic(fmt.Errorf("duplicate group name %q in %s", group.Name(), o.Path()))
			}
			groups[group.Name()] = true
			for j, child := range objectList(raw, "attrs", location+".attrs") {
				group.groupAttrs = append(group.groupAttrs, addAttr(child, fmt.Sprintf("%s.attrs[%d]", location, j)))
			}
			result.groups = append(result.groups, group)
		}
		if o.SetName() != "" && !attrs["id"] {
			result.attrs = append(result.attrs, o.newChild(js.Object{"name": "id", "type": TypeID}, "attrs"))
		}
		return result
	})
}

func (o *node) Items() []Item {
	return cached(o, cacheItems, func() []Item {
		if o.section != "models" && o.Kind() != KindEnum {
			return nil
		}
		items := declarations[Item](o, "items")
		seen := map[string]string{}
		for i, item := range items {
			entry := item.(*node)
			id, explicit := entry.raw["id"]
			if !explicit {
				id = uint64(i + 1)
			}
			if id == nil {
				panic(fmt.Errorf("%s.id must not be null", item.Path()))
			}
			unsigned := validationInteger(id, false)
			if o.Kind() == KindEnum && !unsigned {
				panic(fmt.Errorf("%s.id must be an unsigned integer", item.Path()))
			}
			key := fmt.Sprintf("%T:%v", id, id)
			if unsigned {
				key = fmt.Sprintf("number:%v", id)
			}
			if previous, ok := seen[key]; ok {
				panic(fmt.Errorf("%s and %s have duplicate item id %v", previous, item.Path(), id))
			}
			seen[key] = item.Path()
			entry.itemID = id
		}
		return items
	})
}

func (o *node) Indexes() []Index {
	return cached(o, cacheIndexes, func() []Index {
		if o.section != "models" {
			return nil
		}
		result := declarations[Index](o, "indexes")
		for _, attr := range o.Attrs() {
			indexType := attr.IndexType()
			if indexType == "" {
				continue
			}
			var exists bool
			for _, index := range result {
				if index.IndexType() != indexType {
					continue
				}
				names := stringList(index.Raw()["attrs"], index.Path()+".attrs")
				if len(names) == 1 && names[0] == attr.Name() && index.Source() == attr.Source() {
					exists = true
					break
				}
			}
			if exists {
				continue
			}
			index := o.newChild(js.Object{"attrs": []any{attr.Name()}, "type": string(indexType)}, "indexes")
			if findByName(result, index.Name()) != nil {
				panic(fmt.Errorf("%s: generated index %q conflicts with a declared index", o.Path(), index.Name()))
			}
			result = append(result, index)
		}
		return result
	})
}

func (o *node) Source() Source {
	return cached(o, cacheSource, func() Source {
		if o.section == "items" {
			if o.parent.Kind() == KindEnum {
				return o.enumSource()
			}
			return o.parent.Source()
		}
		if o.section != "models" && o.section != "attrs" && o.section != "indexes" {
			return nil
		}
		if name := o.text("source"); name != "" {
			return o.resolveSource(name)
		}
		if o.section == "indexes" {
			var source Source
			for i, attr := range o.Attrs() {
				if i > 0 && source != attr.Source() {
					panic(fmt.Errorf("%s requires an explicit source for attributes in different sources", o.Path()))
				}
				source = attr.Source()
			}
			return source
		}
		if o.section == "attrs" {
			return o.parent.Source()
		}
		return nil
	})
}

func (o *node) enumSource() Source {
	for _, source := range o.root.Sources() {
		if source.Conn() == "mem:" {
			return source
		}
	}
	return nil
}

func (o *node) resolveSource(name string) Source {
	if result := findByName(o.root.Sources(), name); result != nil {
		return result
	}
	panic(fmt.Errorf("%s references unknown source %q", o.Path(), name))
}

func (o *node) Cache() Source {
	return cached(o, cacheCache, func() Source {
		if o.section != "models" {
			return nil
		}
		if name := o.text("cache"); name != "" {
			return o.resolveSource(name)
		}
		return nil
	})
}

func (o *node) CacheTTL() time.Duration {
	return cached(o, cacheCacheTTL, func() time.Duration {
		if o.section != "models" {
			return 0
		}
		if o.raw["cache_ttl"] == nil {
			return 0
		}
		if o.Cache() == nil {
			panic(fmt.Errorf("%s.cache_ttl requires cache", o.Path()))
		}
		value, err := time.ParseDuration(o.text("cache_ttl"))
		if err != nil || value < 0 {
			panic(fmt.Errorf("%s.cache_ttl must be a non-negative duration", o.Path()))
		}
		return value
	})
}

func (o *node) SetName() string {
	return o.set
}

func (o *node) ID() Attribute {
	return cached(o, cacheID, func() Attribute {
		if o.section != "models" {
			return nil
		}
		id := findByName(o.Attrs(), "id")
		if id != nil {
			typeInfo := id.Type()
			if typeInfo.Target == nil || typeInfo.Target.Kind() != KindType || typeInfo.Collection || typeInfo.Set {
				panic(fmt.Errorf("%s.id must have a scalar type", o.Path()))
			}
		}
		return id
	})
}

func (o *node) Type() TypeInfo {
	return cached(o, cacheType, func() TypeInfo {
		if o.section != "attrs" {
			return TypeInfo{}
		}
		parsed := o.parsedType
		if parsed.isEnum {
			parsed.name = o.path
		}
		result := TypeInfo{
			Expr:       parsed.expression(),
			Collection: parsed.collection,
			Set:        parsed.isSet,
			Length:     parsed.length,
			Precision:  parsed.precision,
			Scale:      parsed.scale,
		}
		for _, target := range o.root.AllTypes() {
			if target.Kind() == KindType && target.Name() == parsed.name {
				result.Target = target
				break
			}
			if target.Kind() != KindType && target.Path() == parsed.name {
				result.Target = target
				break
			}
		}
		if result.Target == nil {
			panic(fmt.Errorf("%s references unknown type %q", o.Path(), parsed.name))
		}
		if result.Set {
			return result
		}
		switch result.Target.Kind() {
		case KindType:
			result.Primitive = result.Target.(Primitive)
		case KindEnum:
			result.Primitive = findByName(o.root.Types(), TypeString)
		case KindModel:
			if id := result.Target.(Model).ID(); id != nil {
				idType := id.Type()
				result.Primitive = idType.Primitive
				result.Length = idType.Length
				result.Precision = idType.Precision
				result.Scale = idType.Scale
			}
		}
		return result
	})
}

func (o *node) Query() string {
	return cached(o, cacheQuery, func() string {
		if o.Kind() != KindSet {
			return ""
		}
		return o.text("query")
	})
}

// warm completes the graph before checking relationships. Forward and cyclic
// model references therefore share the same nodes without recursive parsing.
func (o *node) warm() {
	o.Lookup("")
	var visit func(*node)
	visit = func(current *node) {
		current.validateStructure()
		current.warmCache()
		current.validate()
		for _, child := range current.Children() {
			visit(child.(*node))
		}
	}
	visit(o)
}

func (o *node) warmCache() {
	o.Children()
	switch o.kind {
	case KindConfig:
		o.Types()
		o.AllTypes()
		o.Domains()
		o.Models()
		o.Sources()
	case KindModel:
		o.Domains()
		o.Attrs()
		o.Groups()
		o.Items()
		o.Indexes()
		o.Source()
		o.Cache()
		o.CacheTTL()
		o.ID()
	case KindAttr, KindSet, KindEnum:
		o.Type()
		o.Domains()
		o.Source()
		o.Query()
		if o.kind == KindEnum {
			o.Items()
		}
	case KindItem:
		o.Source()
	case KindIndex:
		o.Attrs()
		o.Source()
	case KindGroup:
		o.Attrs()
	}
}

func (o *node) validateStructure() {
	if o.section == "root" {
		for _, key := range []string{"enums", "types", "kind"} {
			if o.raw.Has(key) {
				panic(fmt.Errorf("root.%s is unsupported; use the models/sources format", key))
			}
		}
		project := o.metadata()
		if project.Has("kind") {
			panic(fmt.Errorf("project.kind is inferred and must not be declared"))
		}
		return
	}
	if o.section == "items" {
		return
	}
	if o.raw.Has("kind") {
		panic(fmt.Errorf("%s.kind is inferred and must not be declared", o.Path()))
	}
	if o.raw.Has("models") {
		panic(fmt.Errorf("%s.models: models may only be declared at the document root", o.Path()))
	}
	if o.section == "models" {
		if isScalarType(o.Name()) || o.Name() == "enum" || !validTypePath(o.Name()) || strings.Contains(o.Name(), ".") {
			panic(fmt.Errorf("%s: invalid or reserved model name", o.Path()))
		}
		for _, key := range []string{"model", "domain", "enums"} {
			if o.raw.Has(key) {
				panic(fmt.Errorf("%s.%s is unsupported in the models/sources format", o.Path(), key))
			}
		}
	}
	if o.section == "attrs" {
		for _, key := range []string{"domain", "enums", "len", "precision", "scale"} {
			if o.raw.Has(key) {
				panic(fmt.Errorf("%s.%s is unsupported; use the canonical type/source fields", o.Path(), key))
			}
		}
	}
}

func or[T comparable](a, b T) (zero T) {
	if a != zero {
		return a
	}
	return b
}

func findByName[T interface{ Name() string }](objects []T, name string) (zero T) {
	for _, value := range objects {
		if value.Name() == name {
			return value
		}
	}
	return zero
}

func cached[T any](o *node, key int, fn func() T) (zero T) {
	value, ok := o.cache[key]
	if ok {
		if value == nil {
			return zero
		}
		return value.(T)
	}
	result := fn()
	if o.cache == nil {
		o.cache = make(map[int]any)
	}
	o.cache[key] = result
	return result
}
