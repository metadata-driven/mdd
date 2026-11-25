package driver

import (
	"context"
	"database/sql"
	"errors"

	"github.com/goldic/js"
)

// ConnectionConstructor opens a connection using the supplied database schema.
type ConnectionConstructor = func(*Database) (Connection, error)

// Connection provides access to a database through transactions.
type Connection interface {
	// OpenTransaction starts a transaction controlled by ctx.
	OpenTransaction(context.Context) (Transaction, error)
	// Close releases the resources held by the connection.
	Close() error
}

// Transaction reads and changes records within a driver transaction.
// Call Commit or Rollback when the transaction is no longer needed.
type Transaction interface {
	// Commit persists the transaction's changes and ends the transaction.
	Commit() error
	// Rollback discards uncommitted changes and ends the transaction.
	Rollback()

	// Get returns ErrNotFound when the record does not exist. Field values use
	// the types described by DataType. Non-empty file values must be read
	// within this transaction.
	Get(table string, id uint64) (DataObject, error)

	// Set inserts a record and returns its generated ID when id is zero.
	// For a nonzero ID it updates or creates that record and returns the ID.
	// Existing fields not present in values are preserved. Values use the types
	// described by DataType. File values are consumed before Set returns.
	Set(table string, id uint64, values DataObject) (uint64, error)

	// Increment adds each numeric value to the corresponding column of a record.
	// A missing record is not an error. Supported columns are IDs, integers and decimals.
	Increment(table string, id uint64, values DataObject) error

	// Delete removes a record. Deleting a missing record succeeds.
	Delete(table string, id uint64) error

	// Fetch stops successfully on ErrBreak and returns the offset of the last delivered record.
	// Other callback errors are returned to the caller.
	// Treat the query and its values as read-only for the duration of the call.
	Fetch(*Query, func(id uint64) error) (nextOffset string, err error)
}

// SQLTransaction supports SQLFilters in Fetch and native statements in FetchSQL.
type SQLTransaction interface {
	Transaction

	// FetchSQL streams a single column of record IDs from a native read query.
	// ErrBreak stops iteration successfully. Other callback errors are returned.
	// The callback runs with the rows open. Use the transaction after FetchSQL returns.
	FetchSQL(statement string, args []any, fn func(id uint64) error) error
}

// SQLExecutor is an optional transaction capability for running native SQL.
type SQLExecutor interface {
	Transaction

	// ExecSQL executes a statement in the current transaction and returns its result.
	ExecSQL(statement string, args ...any) (sql.Result, error)
}

// DataObject maps column names to their values.
type DataObject = js.Object

var (
	// ErrLock indicates that a storage resource is locked.
	ErrLock = errors.New("data is locked")
	// ErrBreak stops a Fetch callback without failing the query.
	ErrBreak = errors.New("break")
	// ErrNotFound indicates that a requested record does not exist.
	ErrNotFound = errors.New("mdd: object not found")
	// ErrSQLUnsupported indicates that the storage driver does not support SQL queries.
	ErrSQLUnsupported = errors.New("driver: SQL queries are not supported")
)

// IsBreak reports whether err is or wraps ErrBreak.
func IsBreak(err error) bool {
	return err == ErrBreak || errors.Is(err, ErrBreak)
}

// IsLockError reports whether err is or wraps ErrLock.
func IsLockError(err error) bool {
	return err == ErrLock || errors.Is(err, ErrLock)
}
