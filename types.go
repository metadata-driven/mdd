package mdd

import (
	"time"

	"github.com/metadata-driven/mdd/driver"
)

type (
	// File is a lazily read file value.
	File = driver.File

	// Time is a date and time value.
	Time = time.Time

	// JSONObject is a JSON object value.
	JSONObject = driver.DataObject
)

const (
	// OpEqual matches any of the supplied values.
	OpEqual = driver.OpEqual
	// OpNotEqual excludes all of the supplied values.
	OpNotEqual = driver.OpNotEqual
	// OpGT matches values greater than the supplied value.
	OpGT = driver.OpGT
	// OpGTE matches values greater than or equal to the supplied value.
	OpGTE = driver.OpGTE
	// OpLT matches values less than the supplied value.
	OpLT = driver.OpLT
	// OpLTE matches values less than or equal to the supplied value.
	OpLTE = driver.OpLTE
	// OpMatch matches text containing the supplied value.
	OpMatch = driver.OpMatch
	// OpContainsAll matches collections containing all supplied values.
	OpContainsAll = driver.OpContainsAll
	// OpContainsAny matches collections containing at least one supplied value.
	OpContainsAny = driver.OpContainsAny
)

// NewFile accepts a string, byte slice, io.Reader, or reader factory.
// A supplied reader is consumed at most once; strings, byte slices, and reader
// factories can be reopened after reading or closing.
// An existing value created by NewFile is returned unchanged.
// A supplied io.ReadCloser is closed after reading starts; calling Close before
// the first read leaves the supplied reader untouched.
func NewFile(value any) File {
	return driver.NewFile(value)
}
