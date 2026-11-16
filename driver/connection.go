package driver

import (
	"context"
	"errors"

	"github.com/goldic/js"
)

type ConnectionConstructor = func(*Database) (Connection, error)

type Connection interface {
	OpenTransaction(context.Context) (Transaction, error)
	Close() error
}

type Transaction interface {
	Commit() error
	Rollback()

	// Get returns ErrNotFound when the record does not exist. Field values use
	// the types described by DataType. Non-empty file values must be read
	// within this transaction.
	Get(table string, id uint64) (DataObject, error)

	// Fetch stops successfully on ErrBreak and returns the offset of the last delivered record.
	// Other callback errors are returned to the caller.
	Fetch(*Query, func(id uint64) error) (nextOffset string, err error)

	// Set inserts a record and returns its generated ID when id is zero.
	// For a nonzero ID it updates or creates that record and returns the ID.
	// Existing fields not present in values are preserved. Values use the types
	// described by DataType. File values are consumed before Set returns.
	Set(table string, id uint64, values DataObject) (uint64, error)
	Increment(table string, id uint64, values DataObject) error
	// Delete removes a record. Deleting a missing record succeeds.
	Delete(table string, id uint64) error

	//Subscribe(*Subscription) error
	//?? Subscribe(q *Query, onUpdate func(uint64), onDisconnect func(error)) (unsubscribe func(), err error)
}

type DataObject = js.Object

var (
	ErrLock     = errors.New("data is locked")
	ErrBreak    = errors.New("break")
	ErrNotFound = errors.New("mdd: object not found")
)

func IsBreak(err error) bool {
	return err == ErrBreak || errors.Is(err, ErrBreak)
}

func IsLockError(err error) bool {
	return err == ErrLock || errors.Is(err, ErrLock)
}
