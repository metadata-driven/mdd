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
	TypeID       DataType = iota // uint64
	TypeBoolean                  // Boolean value.
	TypeInteger                  // Signed 64-bit integer.
	TypeDecimal                  // Exact decimal with precision and scale.
	TypeDatetime                 // Date and time.
	TypeString                   // Variable-length character string.
	TypeBinary                   // In-memory binary data.
	TypeFile                     // Lazily read binary data.
	TypeJSON                     // JSON-compatible structured value.
)
