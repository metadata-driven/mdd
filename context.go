package mdd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"slices"
	"time"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/meta"
	"github.com/metadata-driven/mdd/xquery"
)

type Context struct {
	context.Context
	self          any
	app           appProvider
	schema        *schema
	logger        *log.Logger
	logBase       *log.Logger
	timeoutParent context.Context
	timeoutCancel context.CancelFunc
	txStarted     bool

	objects map[objectKey]*Object
	txs     map[*driver.Database]driver.Transaction

	pending       *Object
	commitQueue   []*Object
	commitWritten map[*Object]bool // Also records which objects are queued.
	committing    bool
	afterCommit   bool
	constant      bool
}

type objectKeyProvider interface {
	objectKey() objectKey
}

type objectKey struct {
	tab *metaTable
	id  uint64 // object id
}

type afterCommitHook interface {
	OnAfterCommit()
	UID() string
}

// MDDApp returns the application that owns the context.
func (ctx *Context) MDDApp() any {
	if ctx == nil || ctx.app == nil {
		return nil
	}
	return ctx.app.getSelf()
}

func (ctx *Context) Meta() meta.Config {
	return ctx.schema.config
}

// Logger returns the context's logger.
func (ctx *Context) Logger() *log.Logger {
	if ctx.logger != nil {
		return ctx.logger
	}
	if ctx.app != nil {
		return ctx.app.Logger()
	}
	return log.Default()
}

// SetLogger configures the logger for this context. A nil logger selects the
// application logger. Prefix is added without changing the supplied logger.
func (ctx *Context) SetLogger(logger *log.Logger, prefix string) {
	if logger == nil {
		if ctx.app != nil {
			logger = ctx.app.Logger()
		} else {
			logger = log.Default()
		}
	}
	ctx.logBase = logger
	ctx.logger = withLogPrefix(logger, prefix)
}

// SetLogPrefix sets the context logger's prefix without changing its base logger.
func (ctx *Context) SetLogPrefix(prefix string) {
	base := ctx.logBase
	if base == nil {
		base = ctx.Logger()
	}
	ctx.SetLogger(base, prefix)
}

// Log writes a message to the context logger.
func (ctx *Context) Log(values ...any) {
	ctx.Logger().Print(values...)
}

// Logf writes a formatted message to the context logger.
func (ctx *Context) Logf(format string, values ...any) {
	ctx.Logger().Printf(format, values...)
}

// SetTimeout sets a timeout for this execution. It must be called before any
// driver transaction is opened. A later call replaces the previous timeout.
func (ctx *Context) SetTimeout(timeout time.Duration) {
	if ctx.txStarted {
		panic("mdd: cannot set timeout after opening a transaction")
	}
	if ctx.timeoutParent == nil {
		ctx.timeoutParent = ctx.Context
	}
	if ctx.timeoutCancel != nil {
		ctx.timeoutCancel()
	}
	ctx.Context, ctx.timeoutCancel = context.WithTimeout(ctx.timeoutParent, timeout)
}

func (ctx *Context) getSelf() any {
	return ctx.self
}

func (ctx *Context) setSelf(self any) {
	ctx.self = self
}

func (ctx *Context) contextCore() *Context {
	return ctx
}

func (ctx *Context) initContext(c context.Context, app appProvider, schema *schema) {
	if c == nil {
		c = context.Background()
	}
	ctx.Context = c
	ctx.app = app
	ctx.logBase = app.Logger()
	ctx.logger = ctx.logBase
	ctx.schema = schema
	ctx.objects = make(map[objectKey]*Object)
	ctx.txs = make(map[*driver.Database]driver.Transaction)
}

func (ctx *Context) metaTableByType(typ reflect.Type) *metaTable {
	return ctx.schema.metaTableByType(typ)
}

func (ctx *Context) transaction(tab *metaTable) driver.Transaction {
	check(ctx.Err())
	if tx := ctx.txs[tab.Database]; tx != nil {
		return tx
	}
	ctx.txStarted = true
	tx := must(ctx.app.getConn(tab.Database).OpenTransaction(ctx.Context))
	ctx.txs[tab.Database] = tx
	return tx
}

func (ctx *Context) checkWritable() {
	check(ctx.Err())
	if ctx.afterCommit {
		panic("mdd: the current context cannot change data during OnAfterCommit")
	}
}

func (ctx *Context) isAfterCommit() bool { return ctx.afterCommit }

func (ctx *Context) Commit() {
	ctx.commit()
}

func (ctx *Context) Rollback() {
	if ctx.committing {
		panic("mdd: cannot roll back during commit")
	}
	ctx.rollback()
}

func (ctx *Context) txSet(obj *Object) {
	ctx.checkWritable()
	if obj.id == 0 && ctx.pending != obj {
		panic("mdd: cannot change an unsaved object while it is being written")
	}
	if ctx.pending != obj {
		ctx.flush()
		ctx.pending = obj
	}
	ctx.track(obj)
}

func (ctx *Context) track(obj *Object) {
	if ctx.commitWritten == nil {
		ctx.commitWritten = make(map[*Object]bool)
	}
	if _, exists := ctx.commitWritten[obj]; !exists {
		ctx.commitWritten[obj] = false
		ctx.commitQueue = append(ctx.commitQueue, obj)
	}
}

func (ctx *Context) txGet(tab *metaTable, obj *Object) driver.DataObject {
	check(ctx.Err())
	newObject := obj.id == 0
	ctx.flush()
	if newObject {
		return nil
	}
	row, err := ctx.transaction(tab).Get(tab.Table.Name, obj.id)
	if errors.Is(err, driver.ErrNotFound) {
		return nil
	}
	check(err)
	return row
}

func (ctx *Context) txDelete(obj *Object, exists bool) {
	ctx.checkWritable()
	if ctx.pending == obj {
		ctx.pending = nil
	} else {
		ctx.flush()
	}
	ctx.track(obj)
	if obj.id == 0 {
		return
	}
	for _, tab := range obj.tab.storage {
		check(ctx.transaction(tab).Delete(tab.Table.Name, obj.id))
	}
	if exists {
		ctx.commitWritten[obj] = true
	}
}

func (ctx *Context) txFetch(tab *metaTable, query *driver.Query, fetcher func(uint64) error) string {
	check(ctx.Err())
	ctx.flush()
	return must(ctx.transaction(tab).Fetch(query, fetcher))
}

func (ctx *Context) flush() {
	check(ctx.Err())
	for ctx.pending != nil {
		check(ctx.Err())
		obj := ctx.pending
		if obj.id == 0 {
			obj.prepareCreated()
		}
		// A lazy value can stage another object while the driver writes.
		ctx.pending = nil
		changes := obj.takeChanges()
		primary := obj.tab
		main := changes
		var others map[*metaTable]driver.DataObject
		for name, value := range changes {
			attr := primary.Attrs[name]
			if tab := attr.storageTable(); tab != primary {
				if others == nil {
					others = make(map[*metaTable]driver.DataObject)
				}
				if others[tab] == nil {
					others[tab] = make(driver.DataObject)
				}
				others[tab][name] = value
			}
		}
		if others != nil {
			main = make(driver.DataObject, len(changes))
			for name, value := range changes {
				if primary.Attrs[name].storageTable() == primary {
					main[name] = value
				}
			}
		}
		id := obj.id
		isNew := id == 0
		if id == 0 || len(main) > 0 {
			id = must(ctx.transaction(primary).Set(primary.Table.Name, id, main))
		}
		for tab, values := range others {
			must(ctx.transaction(tab).Set(tab.Table.Name, id, values))
		}
		if isNew || len(main) > 0 {
			obj.setID(id)
			ctx.objects[objectKey{primary, id}] = obj
		}
		ctx.commitWritten[obj] = true
		if isNew {
			if hook, ok := obj.self.(interface{ OnNew() }); ok {
				hook.OnNew()
			}
		}
	}
}

func (ctx *Context) clearTxs() {
	clear(ctx.txs)
	for _, obj := range ctx.objects {
		obj.clearCache()
	}
	if ctx.pending != nil {
		ctx.pending.clearCache()
	}
	ctx.pending = nil
	clear(ctx.commitWritten)
	clear(ctx.commitQueue)
	ctx.commitQueue = ctx.commitQueue[:0]
}

func (ctx *Context) commit() {
	check(ctx.Err())
	if ctx.committing {
		panic("mdd: recursive commit")
	}
	ctx.committing = true
	defer func() { ctx.committing = false }()
	if hook, ok := ctx.self.(interface{ OnCommit() }); ok {
		hook.OnCommit()
	}
	ctx.flush()
	for i := 0; i < len(ctx.commitQueue); i++ {
		check(ctx.Err())
		obj := ctx.commitQueue[i]
		if obj.deleted {
			continue
		}
		obj.onCommit()

		ctx.flush()
	}
	for _, tx := range ctx.txs {
		check(ctx.Err())
		check(tx.Commit())
	}
	clear(ctx.txs)
	for _, obj := range ctx.objects {
		obj.clearFiles()
	}
	ctx.afterCommit = true
	defer func() {
		ctx.afterCommit = false
		ctx.rollback()
	}()
	var errs []error
	for _, obj := range ctx.commitQueue {
		if !ctx.commitWritten[obj] {
			continue
		}
		if hook, ok := obj.self.(afterCommitHook); ok {
			if err := safeCall(hook.OnAfterCommit); err != nil {
				errs = append(errs, fmt.Errorf("mdd: %s OnAfterCommit failed after commit: %w", hook.UID(), err))
			}
		}
	}
	check(errors.Join(errs...))
}

func (ctx *Context) rollback() {
	defer ctx.clearTxs()
	for _, tx := range ctx.txs {
		tx.Rollback()
	}
}

func (ctx *Context) newObject(tab *metaTable, id uint64) *Object {
	if id == 0 {
		ctx.checkWritable()
	}
	if id != 0 {
		if item := tab.predefined[id]; item != nil {
			return predefinedObject(ctx.schema, tab, item)
		}
		if ctx.constant {
			panic(fmt.Errorf("mdd: %s#%d is not a predefined object", tab.Model.Path(), id))
		}
		if obj := ctx.objects[objectKey{tab, id}]; obj != nil {
			return obj
		}
	}
	obj := must(newModelObject(tab.Type))
	obj.initObject(ctx, tab, id)
	if id == 0 {
		ctx.flush()
		ctx.pending = obj
		ctx.track(obj)
	} else {
		ctx.objects[objectKey{tab, id}] = obj
	}
	return obj
}

func (ctx *Context) newEnum(enum meta.Enum, typ reflect.Type, id uint64) *EnumItem {
	if value := enum.Predefined(id); value != nil {
		return value.(interface{ enumItemCore() *EnumItem }).enumItemCore()
	}
	for _, item := range enum.Items() {
		if storedScalar[uint64](item.ItemID(), item.Path()) == id {
			return predefinedEnumItem(enum, typ.Elem(), item)
		}
	}
	panic(fmt.Errorf("mdd: %s has no item with ID %d", enum.Path(), id))
}

type staticQueryKey struct {
	expr string
	from string
}

type staticQuery struct {
	parsed *xquery.Query
	query  *driver.Query
}

func (ctx *Context) parseQuery(queryExpr string, defaultFrom string, obj any) *driver.Query {
	template := must(ctx.schema.queryTemplates.parse(queryExpr))
	var buf bytes.Buffer
	check(template.Execute(&buf, obj))
	resultExpr := buf.String()
	static := resultExpr == queryExpr
	key := staticQueryKey{expr: queryExpr, from: defaultFrom}
	if static {
		if value, ok := ctx.schema.queries.Load(key); ok {
			cached := value.(*staticQuery)
			if cached.query == nil {
				return ctx.schema.convertQuery(cached.parsed)
			}
			return copyCachedQuery(cached.query)
		}
	}
	queryExpr = resultExpr
	var args xquery.Args
	if defaultFrom != "" {
		args = xquery.Args{"from": defaultFrom}
	}
	q := must(xquery.Parse(queryExpr, args))
	if defaultFrom != "" {
		defaultTable := ctx.schema.tables[defaultFrom]
		queryTable := ctx.schema.tables[q.From]
		if queryTable != nil && queryTable != defaultTable {
			panic(fmt.Errorf("mdd: query source %q does not match %q", q.From, defaultFrom))
		}
	}
	result := ctx.schema.convertQuery(q)
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
	actual, _ := ctx.schema.queries.LoadOrStore(key, cached)
	cached = actual.(*staticQuery)
	if cached.query == nil {
		return result
	}
	return copyCachedQuery(cached.query)
}

func copyCachedQuery(query *driver.Query) *driver.Query {
	result := *query
	result.Order = slices.Clone(query.Order)
	result.Filters = slices.Clone(query.Filters)
	for i := range result.Filters {
		result.Filters[i].Values = slices.Clone(query.Filters[i].Values)
		for j, value := range result.Filters[i].Values {
			if data, ok := value.([]byte); ok {
				result.Filters[i].Values[j] = bytes.Clone(data)
			}
		}
	}
	return &result
}
