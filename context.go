package mdd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"reflect"
	"time"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/meta"
)

type Context struct {
	context.Context

	// Request is the HTTP request associated with the context, if any.
	Request *http.Request

	writer            io.Writer
	httpHeader        http.Header
	parsedFormRequest *http.Request
	responseStarted   bool

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
// application logger. A nonempty prefix is added as [prefix].
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

// SetLogPrefix sets the context logger's prefix as [prefix]. An empty prefix
// removes the context prefix.
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

func (ctx *Context) initContext(c context.Context, app appProvider, schema *schema, w io.Writer, r *http.Request) {
	if r != nil {
		c = r.Context()
	}
	if c == nil {
		c = context.Background()
	}
	ctx.Context = c
	ctx.SetWriter(w)
	ctx.Request = r
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

func (ctx *Context) transaction(db *driver.Database) driver.Transaction {
	check(ctx.Err())
	if tx := ctx.txs[db]; tx != nil {
		return tx
	}
	ctx.txStarted = true
	tx := must(ctx.app.getConn(db).OpenTransaction(ctx.Context))
	ctx.txs[db] = tx
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

// SQLExecute runs a native SQL statement in the transaction for a metadata
// source. It flushes pending model changes first and panics on failure.
func (ctx *Context) SQLExecute(source, statement string, args ...any) sql.Result {
	if ctx.constant {
		panic("mdd: a predefined object has no transaction context")
	}
	ctx.checkWritable()
	for _, db := range ctx.schema.databases {
		if db.Name != source {
			continue
		}
		ctx.flush()
		tx, ok := ctx.transaction(db).(driver.SQLExecutor)
		if !ok {
			panic(driver.ErrSQLUnsupported)
		}
		result, err := tx.ExecSQL(statement, args...)
		ctx.invalidateSourceCache(db)
		return must(result, err)
	}
	panic(fmt.Errorf("mdd: unknown storage source %q", source))
}

func (ctx *Context) invalidateSourceCache(db *driver.Database) {
	for key, obj := range ctx.objects {
		if key.tab.Database == db {
			clear(obj.data)
			obj.loaded = false
			continue
		}
		for name := range obj.data {
			if attr := key.tab.Attrs[name]; attr != nil && attr.table != nil && attr.table.Database == db {
				delete(obj.data, name)
			}
		}
	}
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
	row, err := ctx.transaction(tab.Database).Get(tab.Table.Name, obj.id)
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
		check(ctx.transaction(tab.Database).Delete(tab.Table.Name, obj.id))
	}
	if exists {
		ctx.commitWritten[obj] = true
	}
}

func (ctx *Context) txFetch(tab *metaTable, query *driver.Query, fetcher func(uint64) error) {
	check(ctx.Err())
	ctx.flush()
	tx := ctx.transaction(tab.Database)
	sqlTx, supportsSQL := tx.(driver.SQLTransaction)
	rawSQL := query.RawSQL
	if (rawSQL != nil || len(query.SQLFilters) != 0) && !supportsSQL {
		panic(driver.ErrSQLUnsupported)
	}
	if rawSQL == nil {
		_, err := tx.Fetch(query, fetcher)
		check(err)
		return
	}
	if query.Index != "" || len(query.Joins) != 0 || len(query.Filters) != 0 ||
		len(query.SQLFilters) != 0 || len(query.Order) != 0 || query.Offset != "" {
		panic(errors.New("raw SQL query cannot be combined with an index, joins, filters, ordering, or offset"))
	}
	// Close the SQL rows before delivering IDs so model callbacks can use the transaction.
	var ids []uint64
	check(sqlTx.FetchSQL(rawSQL.Text, rawSQL.Args, func(id uint64) error {
		ids = append(ids, id)
		if query.Limit > 0 && int64(len(ids)) == query.Limit {
			return driver.ErrBreak
		}
		return nil
	}))
	for _, id := range ids {
		if err := fetcher(id); err != nil {
			if driver.IsBreak(err) {
				break
			}
			check(err)
		}
	}
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
			id = must(ctx.transaction(primary.Database).Set(primary.Table.Name, id, main))
		}
		for tab, values := range others {
			must(ctx.transaction(tab.Database).Set(tab.Table.Name, id, values))
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
	for _, obj := range ctx.commitQueue {
		obj.isNew = false
	}
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

// FormValue returns the first value for name from the request's form or URL query.
// It parses each new request on first access and panics if parsing fails.
// A missing value or request returns an empty string.
func (ctx *Context) FormValue(name string) string {
	r := ctx.Request
	if r == nil {
		return ""
	}
	if ctx.parsedFormRequest != r {
		check(r.ParseForm())
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType == "multipart/form-data" {
			check(r.ParseMultipartForm(32 << 20))
		}
		ctx.parsedFormRequest = r
	}
	return r.Form.Get(name)
}

// SetWriter sets the output for response methods. A nil writer selects os.Stdout.
func (ctx *Context) SetWriter(w io.Writer) {
	if w == nil {
		w = os.Stdout
	}
	ctx.writer = w
}

// Header returns the response header or a local header for plain writers.
func (ctx *Context) Header() http.Header {
	if w, ok := ctx.writer.(http.ResponseWriter); ok {
		return w.Header()
	}
	if ctx.httpHeader == nil {
		ctx.httpHeader = make(http.Header)
	}
	return ctx.httpHeader
}

// WriteHeader sends an HTTP status code when the writer supports it.
func (ctx *Context) WriteHeader(statusCode int) {
	if w, ok := ctx.writer.(http.ResponseWriter); ok {
		w.WriteHeader(statusCode)
		if statusCode >= 200 || statusCode == http.StatusSwitchingProtocols {
			ctx.responseStarted = true
		}
	}
}

// Write writes p to the underlying writer.
func (ctx *Context) Write(p []byte) (int, error) {
	ctx.responseStarted = true
	return ctx.writer.Write(p)
}

// WriteText sends a UTF-8 plain text body and returns any write error.
func (ctx *Context) WriteText(text string) error {
	ctx.Header().Set("Content-Type", "text/plain; charset=utf-8")
	ctx.responseStarted = true
	_, err := io.WriteString(ctx.writer, text)
	return err
}

// WriteJSON encodes value before sending a JSON response and returns any error.
func (ctx *Context) WriteJSON(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	ctx.Header().Set("Content-Type", "application/json")
	_, err = ctx.Write(data)
	return err
}
