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

// NewFile accepts a string, byte slice, io.Reader, or reader factory.
// A supplied reader is consumed at most once; strings, byte slices, and reader
// factories can be reopened after reading or closing.
// An existing value created by NewFile is returned unchanged.
// A supplied io.ReadCloser is closed after reading starts; calling Close before
// the first read leaves the supplied reader untouched.
func NewFile(value any) File {
	return driver.NewFile(value)
}
