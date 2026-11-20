package mdd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/meta"
)

type Application[C any] struct {
	self   any
	schema *schema
	logger atomic.Pointer[log.Logger]

	connMu sync.Mutex
	conn   map[*driver.Database]driver.Connection
}

type appProvider interface {
	baseObject
	Open(cfg meta.Config) error
	Logger() *log.Logger
	getConn(*driver.Database) driver.Connection
}

type webContext interface {
	HandleHTTP()
}

// Logger returns the application's logger, defaulting to the standard logger.
func (a *Application[C]) Logger() *log.Logger {
	if logger := a.logger.Load(); logger != nil {
		return logger
	}
	return log.Default()
}

// SetLogger configures the logger inherited by new contexts.
// A nil logger selects the standard logger. A nonempty prefix is added as [prefix].
func (a *Application[C]) SetLogger(logger *log.Logger, prefix string) {
	if logger == nil {
		logger = log.Default()
	}
	a.logger.Store(withLogPrefix(logger, prefix))
}

func (a *Application[C]) getSelf() any {
	return a.self
}

func (a *Application[C]) setSelf(self any) {
	a.self = self
}

func (a *Application[C]) Close() (result error) {
	defer func() {
		if result != nil {
			a.Logger().Printf("mdd: close: %v", result)
		}
	}()
	a.connMu.Lock()
	defer a.connMu.Unlock()
	var errs []error
	for db, conn := range a.conn {
		if err := conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("mdd: close %s: %w", db.Name, err))
		}
		delete(a.conn, db)
	}
	return errors.Join(errs...)
}

func (a *Application[C]) Open(config meta.Config) (err error) {
	defer func() {
		if err != nil {
			a.Logger().Printf("mdd: open: %v", err)
		}
	}()
	schema, err := convertSchema(config)
	if err != nil {
		return fmt.Errorf("mdd: convert metadata: %w", err)
	}
	schema.textTemplates.parser = TemplateEngine.Text
	schema.queryTemplates.parser = TemplateEngine.Query
	if err := schema.warmTemplates(config); err != nil {
		return fmt.Errorf("mdd: %w", err)
	}
	a.conn = map[*driver.Database]driver.Connection{}
	a.schema = schema
	if err := a.initPredefined(); err != nil {
		_ = a.Close()
		return fmt.Errorf("mdd: initialize predefined records: %w", err)
	}
	return
}

func (a *Application[C]) initPredefined() error {
	return safeCall(func() {
		for _, db := range a.schema.databases {
			var tx driver.Transaction
			func() {
				defer func() {
					if tx != nil {
						tx.Rollback()
					}
				}()
				for _, item := range a.schema.items {
					if item.source != db.Name {
						continue
					}
					if tx == nil {
						tx = must(a.getConn(db).OpenTransaction(context.Background()))
					}
					values := make(driver.DataObject)
					for _, col := range item.table.Columns {
						values[col.Name] = driver.Cast(item.values[col.Name], col.Type, false)
					}
					must(tx.Set(item.table.Name, item.id, values))
				}
				if tx != nil {
					check(tx.Commit())
					tx = nil
				}
			}()
		}
	})
}

func (a *Application[C]) Execute(c context.Context, fn func(*C)) error {
	return a.execute(c, nil, nil, "", fn)
}

// HandleHTTP runs the context's HandleHTTP method in a transaction using the
// request's context and writer. It returns an error if the method is missing.
// A nil request uses context.Background. A nil writer uses os.Stdout.
// The context uses the "[http] " log prefix. Responses are written as HandleHTTP runs.
func (a *Application[C]) HandleHTTP(w io.Writer, r *http.Request) error {
	if _, ok := any((*C)(nil)).(webContext); !ok {
		return fmt.Errorf("mdd: context %T must implement HandleHTTP()", (*C)(nil))
	}
	return a.execute(nil, w, r, "http", func(ctx *C) {
		any(ctx).(webContext).HandleHTTP()
	})
}

func (a *Application[C]) execute(c context.Context, w io.Writer, r *http.Request, logPrefix string, fn func(*C)) (err error) {
	var base *Context
	defer func() {
		if base != nil {
			defer func() {
				if base.timeoutCancel != nil {
					base.timeoutCancel()
				}
			}()
		}
		if r := recover(); r != nil {
			err = toError(r)
			logger := a.Logger()
			if base != nil {
				base.rollback()
				logger = base.Logger()
			}
			logger.Printf("mdd: execute: %v", err)
		}
	}()
	ctx := a.newContext(c, w, r)
	base = any(ctx).(interface{ contextCore() *Context }).contextCore()
	base.SetLogPrefix(logPrefix)
	check(base.Err())
	if hook, ok := any(ctx).(interface{ OnInit() }); ok {
		hook.OnInit()
	}
	fn(ctx)
	base.commit()
	return nil
}

// Cron executes fn in a new transaction at each period boundary until c is
// canceled. Runs do not overlap; boundaries missed during a run are skipped.
// Each run uses the "[cron] " log prefix.
// Period boundaries are measured from the Unix epoch. A nonpositive period
// causes a panic. Cron blocks until the context is canceled.
func (a *Application[C]) Cron(c context.Context, period time.Duration, fn func(*C)) {
	if period <= 0 {
		panic("mdd: cron period must be positive")
	}
	if c == nil {
		c = context.Background()
	}
	for c.Err() == nil {
		now := time.Now().UnixNano()
		next := time.Unix(0, now).Add(period - time.Duration(now)%period)
		for dt := time.Until(next); dt > 0 && c.Err() == nil; dt = time.Until(next) {
			time.Sleep(max(10*time.Microsecond, min(15*time.Second, dt)))
		}
		if c.Err() != nil {
			return
		}
		_ = a.execute(c, nil, nil, "cron", fn)
	}
}

func (a *Application[C]) getConn(db *driver.Database) driver.Connection {
	a.connMu.Lock()
	defer a.connMu.Unlock()

	if conn := a.conn[db]; conn != nil {
		return conn
	}
	conn := must(driver.NewConnection(db))
	if conn == nil {
		panic("mdd: invalid connection")
	}
	a.conn[db] = conn
	return conn
}

func (a *Application[C]) newContext(base context.Context, w io.Writer, r *http.Request) *C {
	ctx := newBaseObject(reflect.TypeFor[C]()).(interface{ contextCore() *Context }).contextCore()
	ctx.initContext(base, a, a.schema, w, r)
	return ctx.getSelf().(*C)
}
