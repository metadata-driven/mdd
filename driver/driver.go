package driver

import (
	"fmt"
	"net/url"
	"path"
)

var drivers = map[string]ConnectionConstructor{}

func Register(constructor ConnectionConstructor, scheme ...string) error {
	for _, name := range scheme {
		drivers[name] = constructor
	}
	return nil
}

func NewConnection(d *Database) (Connection, error) {
	var conn string
	if d != nil {
		conn = d.Connection
	}
	if conn == "" {
		conn = "mem:"
	}
	op, err := url.Parse(conn)
	if err != nil {
		return nil, fmt.Errorf("driver: parse connection string: %w", err)
	}
	driver := op.Scheme
	if driver == "" && op.Path != "" {
		if ext := path.Ext(op.Path); ext != "" {
			driver = ext[1:]
			op.Scheme = driver
		}
	}
	constructor := drivers[driver]
	if constructor == nil {
		return nil, fmt.Errorf("driver: driver %q is not registered", driver)
	}
	return constructor(d)
}
