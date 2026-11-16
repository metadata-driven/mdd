package driver

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrFileConsumed indicates that a one-shot file reader cannot be reopened.
var ErrFileConsumed = errors.New("driver: file reader cannot be reopened")

// File is a lazy view of a binary attribute in a transaction.
type File interface {
	io.ReadCloser
	Bytes() []byte
	String() string
}

type fileValue struct {
	open   func() (io.ReadCloser, error)
	reader io.ReadCloser
}

// NewFile accepts a string, byte slice, io.Reader, or reader factory.
// A supplied reader is consumed at most once; strings, byte slices, and reader
// factories can be reopened after reading or closing.
// An existing value created by NewFile is returned unchanged.
// A supplied io.ReadCloser is closed after reading starts; calling Close before
// the first read leaves the supplied reader untouched.
func NewFile(value any) File {
	switch v := value.(type) {
	case nil:
		return &fileValue{}
	case string:
		return &fileValue{open: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(v)), nil
		}}
	case []byte:
		if v == nil {
			return &fileValue{}
		}
		return &fileValue{open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(v)), nil
		}}
	case func() (io.ReadCloser, error):
		return &fileValue{open: v}
	case *fileValue:
		if v == nil {
			return &fileValue{}
		}
		return v
	case io.Reader:
		used := false
		return &fileValue{open: func() (io.ReadCloser, error) {
			if used {
				return nil, ErrFileConsumed
			}
			used = true
			if reader, ok := v.(io.ReadCloser); ok {
				return reader, nil
			}
			return io.NopCloser(v), nil
		}}
	default:
		panic(fmt.Errorf("driver: unsupported file value %T", value))
	}
}

// FileBytes consumes a file input and returns its bytes.
func FileBytes(value any) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	case io.Reader:
		return io.ReadAll(v)
	default:
		return nil, fmt.Errorf("driver: unsupported file value %T", value)
	}
}

func (b *fileValue) String() string {
	return string(b.Bytes())
}

// Read opens the value on first use and reads from the same stream thereafter.
func (b *fileValue) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	reader, err := b.getReader()
	if err != nil {
		return 0, err
	}
	if reader == nil {
		return 0, io.EOF
	}
	n, err := reader.Read(p)
	if err != nil {
		if closeErr := b.Close(); err == io.EOF && closeErr != nil {
			return n, closeErr
		}
	}
	return n, err
}

// Close closes the stream used by Read, if one was opened.
func (b *fileValue) Close() error {
	if b == nil || b.reader == nil {
		return nil
	}
	reader := b.reader
	b.reader = nil
	return reader.Close()
}

func (b *fileValue) getReader() (io.ReadCloser, error) {
	if b == nil {
		return nil, nil
	}
	if b.reader == nil && b.open != nil {
		reader, err := b.open()
		if err != nil {
			if reader != nil {
				_ = reader.Close()
			}
			return nil, err
		}
		b.reader = reader
	}
	return b.reader, nil
}

// Bytes reads the remaining value and closes its reader.
// It returns nil for an absent value; an empty value returns an empty slice.
func (b *fileValue) Bytes() []byte {
	reader, err := b.getReader()
	if err != nil {
		panic(err)
	}
	if reader == nil {
		return nil
	}
	defer func() {
		if err := b.Close(); err != nil {
			panic(err)
		}
	}()
	data, err := io.ReadAll(b)
	if err != nil {
		panic(err)
	}
	return data
}
