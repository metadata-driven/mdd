package driver

import "time"

// Decimal is an exact decimal value in base-10 notation.
type Decimal = string

// Time is a UTC timestamp with microsecond precision.
type Time = time.Time

// JSON is a decoded JSON value; numbers use json.Number to preserve precision.
type JSON = any

// DataType identifies a database-independent column type. Values exchanged with
// drivers use uint64 for IDs, bool for booleans, int64 for integers, Decimal
// for decimals, Time for timestamps, string for text, []byte for binary data,
// and File for file data. An absent binary, file, or JSON value is nil. JSON
// values are decoded Go JSON values: nil, bool, string, json.Number, []any, or
// map[string]any.
type DataType int

const (
	// TypeID is an unsigned 64-bit record identifier or reference.
	TypeID DataType = iota
	// TypeBoolean is a Boolean value.
	TypeBoolean
	// TypeInteger is a signed 64-bit integer.
	TypeInteger
	// TypeDecimal is an exact decimal with precision and scale.
	TypeDecimal
	// TypeDatetime is a UTC date and time.
	TypeDatetime
	// TypeString is a variable-length character string.
	TypeString
	// TypeBinary is in-memory binary data.
	TypeBinary
	// TypeFile is lazily read binary data.
	TypeFile
	// TypeJSON is a JSON-compatible structured value.
	TypeJSON
)
