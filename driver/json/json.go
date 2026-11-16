// Package json registers a JSON file-backed storage driver.
package json

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/driver/json/filedb"
)

var _ = driver.Register(open, "json")

func open(schema *driver.Database) (driver.Connection, error) {
	return filedb.Open(schema, filedb.Codec{
		Marshal: func(value any) ([]byte, error) {
			data, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				return nil, err
			}
			return append(data, '\n'), nil
		},
		Unmarshal: func(data []byte, out any) error {
			d := json.NewDecoder(bytes.NewReader(data))
			d.UseNumber()
			if err := d.Decode(out); err != nil {
				return err
			}
			var extra any
			if err := d.Decode(&extra); err != io.EOF {
				if err == nil {
					return fmt.Errorf("multiple JSON documents")
				}
				return err
			}
			return nil
		},
	})
}
