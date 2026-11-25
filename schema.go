package mdd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
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
	for _, condition := range q.Where {
		name := condition.Field.String()
		result.Filters = append(result.Filters, s.queryFilter(tab, result, name, condition.Op, condition.Values))
	}
	for _, order := range q.Order {
		name := order.Field.String()
		source, column, _ := s.resolveQueryField(tab, result, name)
		result.Order = append(result.Order, driver.QueryOrder{Source: source, Column: column, Desc: order.Desc})
	}
	return result
}

// resolveQueryField follows model references, adds the required joins, and
// returns the source alias, local column name, and attribute metadata.
func (s *schema) resolveQueryField(root *metaTable, query *driver.Query, name string) (string, string, *metaAttribute) {
	if name == "" {
		panic(fmt.Errorf("mdd: empty query field on %s", root.Name))
	}
	current := root
	source := ""
	end := 0
	for part := range xquery.Field(name).Parts() {
		end += len(part)
		last := end == len(name)
		if last && part == "id" {
			return source, "id", nil
		}
		attr := current.Attrs[part]
		if attr == nil || attr.column == nil || attr.table == nil || attr.table.Table == nil {
			panic(fmt.Errorf("mdd: unknown query field %s.%s", current.Name, part))
		}
		if attr.table.Database != root.Database {
			panic(fmt.Errorf("mdd: query field %s.%s is stored in a different database", root.Name, name))
		}
		if attr.table.Table != current.Table {
			panic(fmt.Errorf("mdd: query field %s.%s is not stored in its model's primary table", root.Name, name))
		}
		if last {
			return source, attr.column.Name, attr
		}
		alias := name[:end]
		info := attr.Type()
		if info.Collection || info.Set || info.Target == nil || info.Target.Kind() != meta.KindModel {
			panic(fmt.Errorf("mdd: query path %s.%s is not a single model reference", root.Name, alias))
		}
		target := s.tables[info.Target.Path()]
		if target == nil || target.Table == nil || target.Database == nil {
			panic(fmt.Errorf("mdd: query reference %s has no storage table", info.Target.Path()))
		}
		if target.Database != root.Database {
			panic(fmt.Errorf("mdd: query path %s.%s joins different databases", root.Name, alias))
		}
		addQueryJoins(query, driver.QueryJoin{
			Source: source,
			Column: attr.column.Name,
			Table:  target.Table.Name,
			Alias:  alias,
		})
		current = target
		source = alias
		end++
	}
	panic(fmt.Errorf("mdd: invalid query field %s.%s", root.Name, name))
}

func (s *schema) queryFilter(root *metaTable, query *driver.Query, name, op string, values []any) driver.QueryFilter {
	source, column, attr := s.resolveQueryField(root, query, name)
	if op == driver.OpContainsAll || op == driver.OpContainsAny {
		if attr == nil || !attr.Type().Collection {
			panic(fmt.Errorf("mdd: operator %s requires a collection: %s.%s", op, root.Name, name))
		}
		if len(values) == 0 {
			panic(fmt.Errorf("mdd: operator %s requires at least one value", op))
		}
		return driver.QueryFilter{Source: source, Column: column, Op: op, Values: castCollectionQueryValues(attr, values)}
	}
	return driver.QueryFilter{Source: source, Column: column, Op: op, Values: castQueryValues(attr, values)}
}

type staticQuery struct {
	parsed *xquery.Query
	query  *driver.Query
}

func (s *schema) parseQuery(queryExpr string, defaultFrom string, obj any) *driver.Query {
	template := must(s.queryTemplates.parse(queryExpr))
	var buf bytes.Buffer
	check(template.Execute(&buf, obj))
	resultExpr := buf.String()
	static := resultExpr == queryExpr
	key := [2]string{queryExpr, defaultFrom}
	if static {
		if value, ok := s.queries.Load(key); ok {
			cached := value.(*staticQuery)
			if cached.query == nil {
				return s.convertQuery(cached.parsed)
			}
			return copyPtr(cached.query)
		}
	}
	queryExpr = resultExpr
	var args xquery.Args
	if defaultFrom != "" {
		args = xquery.Args{"from": defaultFrom}
	}
	q := must(xquery.Parse(queryExpr, args))
	if defaultFrom != "" {
		defaultTable := s.tables[defaultFrom]
		queryTable := s.tables[q.From]
		if queryTable != nil && queryTable != defaultTable {
			panic(fmt.Errorf("mdd: query source %q does not match %q", q.From, defaultFrom))
		}
	}
	result := s.convertQuery(q)
	if !static {
		return result
	}
	cached := &staticQuery{parsed: q, query: result}
	for _, filter := range result.Filters {
		for _, value := range filter.Values {
			if _, ok := value.(driver.File); ok {
				// A file reader has mutable state, so recreate it from the parsed query.
				cached.query = nil
			}
		}
	}
	if cached.query != nil {
		// Cached elements are read-only. Appending must allocate separate storage.
		result.Joins = slices.Clip(result.Joins)
		result.Order = slices.Clip(result.Order)
		result.Filters = slices.Clip(result.Filters)
		result.SQLFilters = slices.Clip(result.SQLFilters)
	}
	actual, _ := s.queries.LoadOrStore(key, cached)
	cached = actual.(*staticQuery)
	if cached.query == nil {
		return result
	}
	return copyPtr(cached.query)
}

func addQueryJoins(query *driver.Query, joins ...driver.QueryJoin) {
	for _, join := range joins {
		found := false
		for _, existing := range query.Joins {
			if existing.Alias == join.Alias {
				if existing != join {
					panic(fmt.Errorf("mdd: conflicting query join %s", join.Alias))
				}
				found = true
				break
			}
		}
		if !found {
			query.Joins = append(query.Joins, join)
		}
	}
}

func castQueryValues(attr *metaAttribute, values []any) []any {
	typ := driver.TypeID
	if attr != nil {
		typ = attr.column.Type
	}
	result := make([]any, len(values))
	for i, value := range values {
		if attr != nil && attr.Type().Target != nil && attr.Type().Target.Kind() == meta.KindEnum {
			if itemName, ok := value.(string); ok {
				for _, item := range attr.Type().Target.(meta.Enum).Items() {
					if item.Name() == itemName || item.Path() == itemName {
						value = item.ItemID()
						break
					}
				}
			}
		}
		value = driver.Cast(value, typ, false)
		switch typ {
		case driver.TypeFile:
			value = must(driver.FileBytes(value))
		}
		result[i] = value
	}
	return result
}

func collectionElementType(info meta.TypeInfo) driver.DataType {
	if info.IsReference() {
		return driver.TypeID
	}
	switch info.PrimitiveTypeName() {
	case meta.TypeID:
		return driver.TypeID
	case meta.TypeBool:
		return driver.TypeBoolean
	case meta.TypeInt:
		return driver.TypeInteger
	case meta.TypeNumber:
		return driver.TypeDecimal
	case meta.TypeDatetime:
		return driver.TypeDatetime
	case meta.TypeString:
		return driver.TypeString
	case meta.TypeBinary:
		return driver.TypeBinary
	default:
		panic(fmt.Errorf("mdd: unsupported collection type %s", info.Expr))
	}
}

func castCollectionQueryValues(attr *metaAttribute, values []any) []any {
	info := attr.Type()
	typ := collectionElementType(info)
	items := make([]any, len(values))
	for i, value := range values {
		if info.Target.Kind() == meta.KindEnum {
			if name, ok := value.(string); ok {
				for _, item := range info.Target.(meta.Enum).Items() {
					if item.Name() == name || item.Path() == name {
						value = item.ItemID()
						break
					}
				}
			}
		}
		items[i] = value
	}
	result := driver.Cast(items, typ, true).([]any)
	for i, value := range result {
		switch typ {
		case driver.TypeID, driver.TypeInteger:
			result[i] = driver.Cast(value, typ, false)
		case driver.TypeDecimal:
			if value == nil {
				result[i] = json.Number("0")
			}
		case driver.TypeString, driver.TypeBoolean:
			if value == nil {
				result[i] = driver.Cast(nil, typ, false)
			}
		}
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

func (table *metaTable) hasAttr(name string, typ driver.DataType) bool {
	attr := table.Attrs[name]
	return attr != nil && attr.column != nil && attr.column.Type == typ && !attr.Type().IsReference()
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
	table  *driver.Table
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
	for _, database := range c.result.databases {
		database.TableByName("")
		for _, table := range database.Tables {
			table.ColumnByName("")
			table.IndexByName("")
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
		table.Columns = append(table.Columns, col)
		tab.Attrs[attr.Name()].column = col
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
			name := attr.Name()
			if name != "id" && columnByName(table, name) == nil {
				return fmt.Errorf("schema: index %s requires column %q in table %q in source %q", idx.Path(), name, table.Name, idx.Source().Name())
			}
			names = append(names, name)
		}
		if indexByName(table, idx.Name()) != nil {
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
				if table != nil && columnByName(table, key) != nil {
					if rows[source] == nil {
						rows[source] = driver.DataObject{}
					}
					rows[source][key] = val
				}
			}
		}
		for _, db := range c.result.databases {
			if values, ok := rows[db.Name]; ok {
				c.result.items = append(c.result.items, schemaItem{source: db.Name, table: c.tables[db.Name][setName], id: c.itemIDs[item.Path()], values: values, path: item.Path()})
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

func columnByName(table *driver.Table, name string) *driver.Column {
	for _, col := range table.Columns {
		if col.Name == name {
			return col
		}
	}
	return nil
}

func indexByName(table *driver.Table, name string) *driver.Index {
	for _, index := range table.Indexes {
		if index.Name == name {
			return index
		}
	}
	return nil
}
