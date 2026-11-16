package mdd

import (
	"fmt"
	"maps"
	"reflect"
	"strconv"
	"sync"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/meta"
	"github.com/metadata-driven/mdd/xquery"
)

// schema contains storage schemas and predefined records.
type schema struct {
	config         meta.Config
	databases      []*driver.Database
	items          []schemaItem
	tables         map[string]*metaTable
	tablesByType   map[reflect.Type]*metaTable
	queries        sync.Map
	textTemplates  templateCache
	queryTemplates templateCache
}

func (s *schema) metaTableByType(typ reflect.Type) *metaTable {
	tab, ok := s.tablesByType[typ]
	if !ok {
		panic(fmt.Errorf("mdd: unknown model type %v", typ))
	}
	if tab == nil {
		panic(fmt.Errorf("mdd: multiple models registered for type %v", typ))
	}
	return tab
}

func (s *schema) convertQuery(q *xquery.Query) *driver.Query {
	tab := s.tables[q.From]
	if tab == nil || tab.Table == nil {
		panic(fmt.Errorf("mdd: unknown query source %q", q.From))
	}
	if len(q.Select) != 0 {
		panic("mdd: select is not supported in set queries")
	}
	result := &driver.Query{Table: tab.Table.Name, Limit: q.Limit}
	if q.Offset != nil {
		result.Offset = fmt.Sprint(q.Offset)
	}
	field := func(name string) *metaAttribute {
		attr := tab.Attrs[name]
		if name == "id" {
			return attr
		}
		if attr == nil || attr.column == nil || attr.table != tab {
			panic(fmt.Errorf("mdd: unknown query field %s.%s", tab.Name, name))
		}
		return attr
	}
	for _, condition := range q.Where {
		name := condition.Field.String()
		attr := field(name)
		typ := driver.TypeID
		if name != "id" {
			typ = attr.column.Type
		}
		values := make([]any, len(condition.Values))
		for i, value := range condition.Values {
			if attr != nil && attr.Type().Target.Kind() == meta.KindEnum {
				if itemName, ok := value.(string); ok {
					for _, item := range attr.Type().Target.(meta.Enum).Items() {
						if item.Name() == itemName || item.Path() == itemName {
							value = item.ItemID()
							break
						}
					}
				}
			}
			values[i] = driver.Cast(typ, value)
		}
		result.AddFilter(name, condition.Op, values...)
	}
	for _, order := range q.Order {
		name := order.Field.String()
		field(name)
		result.Order = append(result.Order, driver.QueryOrder{Column: name, Desc: order.Desc})
	}
	return result
}

// metaTable maps a model or one of its attributes to its storage table.
// Name is the model name. Type is populated when a model type lookup is supplied.
// Database and Table may be nil when the model has no corresponding storage.
// Attrs maps attribute names to their metadata and primary source tables.
type metaTable struct {
	Name        string
	Model       meta.Model
	SourceName  string
	Source      meta.Source
	Database    *driver.Database
	Table       *driver.Table
	Type        reflect.Type
	Attrs       map[string]*metaAttribute
	storage     []*metaTable
	created     *metaAttribute
	updated     *metaAttribute
	title       Template
	description Template
	icon        Template
	predefined  map[uint64]meta.Item
}

type metaAttribute struct {
	meta.Attribute
	table  *metaTable
	column *driver.Column
}

func (a *metaAttribute) storageTable() *metaTable {
	if a.table == nil || a.table.Table == nil || a.table.Database == nil {
		panic(fmt.Errorf("mdd: %s has no storage table", a.Path()))
	}
	return a.table
}

// schemaItem is a predefined record for one source and table.
// Its values contain only fields stored in that source and exclude the ID.
type schemaItem struct {
	source string
	table  string
	id     uint64
	values driver.DataObject
	path   string
}

func convertSchema(cfg meta.Config) (result *schema, err error) {
	if cfg == nil {
		return nil, fmt.Errorf("schema: config is nil")
	}
	// Metadata implementations can report invalid relationships through a panic.
	defer func() {
		if v := recover(); v != nil {
			if e, ok := v.(error); ok {
				err = fmt.Errorf("schema: %w", e)
			} else {
				err = fmt.Errorf("schema: %v", v)
			}
			result = nil
		}
	}()
	c := &converter{
		result: &schema{
			config:       cfg,
			tables:       map[string]*metaTable{},
			tablesByType: map[reflect.Type]*metaTable{},
		},
		databases: map[string]*driver.Database{},
		tables:    map[string]map[string]*driver.Table{},
		itemIDs:   map[string]uint64{},
	}
	c.owners = map[string]map[string]string{}
	for _, source := range cfg.Sources() {
		if source == nil || source.Name() == "" {
			return nil, fmt.Errorf("schema: invalid source")
		}
		if c.databases[source.Name()] != nil {
			return nil, fmt.Errorf("schema: duplicate source %q", source.Name())
		}
		db := &driver.Database{Name: source.Name(), Connection: source.Conn(), Options: copyOptions(source.Options())}
		c.result.databases = append(c.result.databases, db)
		c.databases[source.Name()] = db
		c.tables[source.Name()] = map[string]*driver.Table{}
		c.owners[source.Name()] = map[string]string{}
	}
	// Collect normalized item IDs before resolving references to predefined items.
	for _, model := range cfg.Models() {
		if e := c.allocate(model.Items()); e != nil {
			return nil, e
		}
		for _, attr := range model.Attrs() {
			if attr.Kind() == meta.KindEnum {
				if e := c.allocate(attr.(meta.Enum).Items()); e != nil {
					return nil, e
				}
			}
		}
	}
	for _, model := range cfg.Models() {
		if e := c.convertModel(model); e != nil {
			return nil, e
		}
	}
	return c.result, nil
}

type converter struct {
	result    *schema
	databases map[string]*driver.Database
	tables    map[string]map[string]*driver.Table
	itemIDs   map[string]uint64
	owners    map[string]map[string]string
}

func copyOptions(in map[string]any) driver.Options {
	if len(in) == 0 {
		return nil
	}
	out := driver.Options{}
	maps.Copy(out, in)
	return out
}

func (c *converter) allocate(items []meta.Item) error {
	seen := map[uint64]string{}
	for _, item := range items {
		v := item.ItemID()
		if v == nil {
			return fmt.Errorf("schema: %s has no normalized id", item.Path())
		}
		id, err := strconv.ParseUint(fmt.Sprint(v), 10, 64)
		if err != nil || id == 0 {
			return fmt.Errorf("schema: %s has invalid uint64 id %v", item.Path(), v)
		}
		if prior, ok := seen[id]; ok {
			return fmt.Errorf("schema: %s and %s have duplicate id %d", prior, item.Path(), id)
		}
		seen[id] = item.Path()
		c.itemIDs[item.Path()] = id
	}
	return nil
}
func (c *converter) ensureTable(source meta.Source, name string, owner string) (*driver.Table, error) {
	if source == nil {
		return nil, fmt.Errorf("schema: %s has no source", owner)
	}
	db := c.databases[source.Name()]
	if db == nil {
		return nil, fmt.Errorf("schema: %s has unknown source %q", owner, source.Name())
	}
	t := c.tables[source.Name()][name]
	if t != nil && c.owners[source.Name()][name] != owner {
		return nil, fmt.Errorf("schema: %s and %s use table %q in source %q", owner, c.owners[source.Name()][name], name, source.Name())
	}
	if t == nil {
		t = &driver.Table{Name: name}
		c.tables[source.Name()][name] = t
		c.owners[source.Name()][name] = owner
		db.Tables = append(db.Tables, t)
	}
	return t, nil
}
func column(attr meta.Attribute) (*driver.Column, error) {
	info := attr.Type()
	col := &driver.Column{Name: attr.Name(), Options: copyOptions(attr.Options())}
	if info.Collection {
		col.Type = driver.TypeJSON
		return col, nil
	}
	if info.Target != nil && info.Target.Kind() == meta.KindEnum {
		col.Type = driver.TypeID
		return col, nil
	}
	if info.Primitive == nil {
		return nil, fmt.Errorf("schema: %s has no storage type", attr.Path())
	}
	switch info.PrimitiveTypeName() {
	case meta.TypeID:
		col.Type = driver.TypeID
	case meta.TypeBool:
		col.Type = driver.TypeBoolean
	case meta.TypeInt:
		col.Type = driver.TypeInteger
	case meta.TypeNumber:
		col.Type = driver.TypeDecimal
		col.Precision = info.Precision
		col.Scale = info.Scale
	case meta.TypeDatetime:
		col.Type = driver.TypeDatetime
	case meta.TypeString:
		col.Type = driver.TypeString
		col.Length = info.Length
	case meta.TypeBinary:
		col.Type = driver.TypeBinary
		col.Length = info.Length
	case meta.TypeFile:
		col.Type = driver.TypeFile
	case meta.TypeJSON:
		col.Type = driver.TypeJSON
	default:
		return nil, fmt.Errorf("schema: %s has unsupported type %q", attr.Path(), info.Expr)
	}
	return col, nil
}
func addColumn(table *driver.Table, col *driver.Column) *driver.Column {
	if existing := table.ColumnByName(col.Name); existing != nil {
		return existing
	}
	table.Columns = append(table.Columns, col)
	return col
}
func (c *converter) convertModel(model meta.Model) error {
	typ := model.ModelType()
	if typ != nil {
		if _, err := newModelObject(typ); err != nil {
			return fmt.Errorf("schema: model %s: %w", model.Path(), err)
		}
	}
	bySource := make(map[string]*metaTable)
	newTable := func(source meta.Source) *metaTable {
		tab := &metaTable{Name: model.Name(), Model: model, Source: source, Type: typ}
		if source != nil {
			tab.SourceName = source.Name()
			tab.Database = c.databases[tab.SourceName]
			bySource[tab.SourceName] = tab
		}
		return tab
	}
	tab := newTable(model.Source())
	for _, item := range model.Items() {
		if tab.predefined == nil {
			tab.predefined = make(map[uint64]meta.Item)
		}
		tab.predefined[c.itemIDs[item.Path()]] = item
	}
	tab.Attrs = make(map[string]*metaAttribute, len(model.Attrs()))
	for _, attr := range model.Attrs() {
		tab.Attrs[attr.Name()] = &metaAttribute{Attribute: attr}
	}
	c.result.tables[model.Name()] = tab
	if typ != nil {
		if _, exists := c.result.tablesByType[typ]; exists {
			// A nil entry marks a type registered for multiple models.
			c.result.tablesByType[typ] = nil
		} else {
			c.result.tablesByType[typ] = tab
		}
	}

	setName := model.SetName()
	if setName == "" {
		if len(model.Items()) > 0 {
			return fmt.Errorf("schema: %s has items but no set", model.Path())
		}
		return nil
	}
	c.result.tables[setName] = tab

	id := model.ID()
	if id == nil || id.Type().PrimitiveTypeName() != meta.TypeID {
		return fmt.Errorf("schema: %s requires a uint64 id", model.Path())
	}
	if model.Source() != nil {
		table, err := c.ensureTable(model.Source(), setName, model.Path())
		if err != nil {
			return err
		}
		tab.Table = table
	}
	for _, attr := range model.Attrs() {
		if attr.Name() == "id" || attr.Type().Set {
			continue
		}
		table, err := c.ensureTable(attr.Source(), setName, model.Path())
		if err != nil {
			return err
		}
		attrTable := bySource[attr.Source().Name()]
		if attrTable == nil {
			attrTable = newTable(attr.Source())
		}
		attrTable.Table = table
		tab.Attrs[attr.Name()].table = attrTable
		col, err := column(attr)
		if err != nil {
			return err
		}
		tab.Attrs[attr.Name()].column = addColumn(table, col)
	}
	if attr := tab.Attrs["created"]; attr != nil && attr.column != nil && attr.column.Type == driver.TypeDatetime {
		tab.created = attr
	}
	if attr := tab.Attrs["updated"]; attr != nil && attr.column != nil && attr.column.Type == driver.TypeDatetime {
		tab.updated = attr
	}
	for _, idx := range model.Indexes() {
		kind := idx.IndexType()
		var indexType driver.IndexType
		switch kind {
		case meta.IndexTypeRegular:
			indexType = driver.IndexTypeIndex
		case meta.IndexTypeUnique:
			indexType = driver.IndexTypeUnique
		case meta.IndexTypeFulltext:
			indexType = driver.IndexTypeFulltext
		default:
			return fmt.Errorf("schema: %s has unsupported index type %q", idx.Path(), kind)
		}
		table, err := c.ensureTable(idx.Source(), setName, model.Path())
		if err != nil {
			return err
		}
		var names []string
		for _, attr := range idx.Attrs() {
			if attr.Name() == "id" {
				names = append(names, "id")
				continue
			}
			if kind == meta.IndexTypeUnique && attr.Source() != idx.Source() {
				return fmt.Errorf("schema: unique index %s requires its attributes in the index source", idx.Path())
			}
			col, e := column(attr)
			if e != nil {
				return e
			}
			addColumn(table, col)
			names = append(names, col.Name)
		}
		if table.IndexByName(idx.Name()) != nil {
			return fmt.Errorf("schema: duplicate index %s", idx.Path())
		}
		table.Indexes = append(table.Indexes, &driver.Index{Name: idx.Name(), Type: indexType, Columns: names, Options: copyOptions(idx.Options())})
	}
	if source := id.Source(); source != nil {
		if table := c.tables[source.Name()][setName]; table != nil && c.owners[source.Name()][setName] == model.Path() {
			idTable := bySource[source.Name()]
			if idTable == nil {
				idTable = newTable(source)
			}
			idTable.Table = table
			tab.Attrs[id.Name()].table = idTable
		}
	}
	tab.storage = []*metaTable{tab}
	seen := map[*metaTable]bool{tab: true}
	for _, attr := range model.Attrs() {
		storage := tab.Attrs[attr.Name()].table
		if storage != nil && !seen[storage] {
			seen[storage] = true
			tab.storage = append(tab.storage, storage)
		}
	}
	for _, item := range model.Items() {
		rows := map[string]driver.DataObject{}
		for source, tables := range c.tables {
			if tables[setName] != nil && c.owners[source][setName] == model.Path() {
				rows[source] = driver.DataObject{}
			}
		}
		if len(rows) == 0 {
			return fmt.Errorf("schema: %s has no storage source", item.Path())
		}
		for key, val := range item.Data() {
			attr := tab.Attrs[key]
			if attr == nil || attr.Type().Set {
				return fmt.Errorf("schema: %s has invalid field %q", item.Path(), key)
			}
			val, err := c.itemValue(attr, val)
			if err != nil {
				return err
			}
			for source, tables := range c.tables {
				table := tables[setName]
				if table != nil && table.ColumnByName(key) != nil {
					if rows[source] == nil {
						rows[source] = driver.DataObject{}
					}
					rows[source][key] = val
				}
			}
		}
		for _, db := range c.result.databases {
			if values, ok := rows[db.Name]; ok {
				c.result.items = append(c.result.items, schemaItem{source: db.Name, table: setName, id: c.itemIDs[item.Path()], values: values, path: item.Path()})
			}
		}
	}
	return nil
}
func (c *converter) itemValue(attr meta.Attribute, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	info := attr.Type()
	if info.Collection {
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.Array && rv.Kind() != reflect.Slice {
			return nil, fmt.Errorf("schema: %s requires a list", attr.Path())
		}
		out := make([]any, rv.Len())
		for i := range out {
			value, err := c.singleValue(info, rv.Index(i).Interface())
			if err != nil {
				return nil, fmt.Errorf("schema: %s: %w", attr.Path(), err)
			}
			if info.Target.Kind() == meta.KindType && info.PrimitiveTypeName() == meta.TypeBinary && value != nil {
				if text, ok := value.(string); ok {
					value = []byte(text)
				}
			}
			out[i] = value
		}
		return out, nil
	}
	value, err := c.singleValue(info, v)
	if err != nil {
		return nil, fmt.Errorf("schema: %s: %w", attr.Path(), err)
	}
	return value, nil
}
func (c *converter) singleValue(info meta.TypeInfo, v any) (any, error) {
	if v == nil || info.Target == nil {
		return v, nil
	}
	switch info.Target.Kind() {
	case meta.KindEnum:
		name, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("invalid enum value %v", v)
		}
		for _, item := range info.Target.(meta.Enum).Items() {
			if name == item.Name() || name == item.Path() {
				return c.itemIDs[item.Path()], nil
			}
		}
		return nil, fmt.Errorf("unknown enum value %q", name)
	case meta.KindModel:
		if name, ok := v.(string); ok {
			if id, exists := c.itemIDs[name]; exists {
				return id, nil
			}
		}
	}
	return v, nil
}
