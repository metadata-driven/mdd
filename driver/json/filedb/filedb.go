// Package filedb provides file persistence for drivers built on mem.
package filedb

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/driver/mem"
)

// Codec converts a file document to and from JSON-compatible values.
type Codec struct {
	Marshal   func(any) ([]byte, error)
	Unmarshal func([]byte, any) error
}

// Open loads a database file and creates a mem connection that saves it on commit.
func Open(schema *driver.Database, codec Codec) (driver.Connection, error) {
	if schema == nil {
		return nil, errors.New("filedb: schema is required")
	}
	if err := mem.ValidateIndexes(schema); err != nil {
		return nil, fmt.Errorf("filedb: %w", err)
	}
	for _, table := range schema.Tables {
		if table == nil {
			return nil, errors.New("filedb: invalid table")
		}
	}
	path, err := filePath(schema.Connection)
	if err != nil {
		return nil, err
	}
	snapshot := &mem.Snapshot{Tables: map[string]map[uint64]driver.DataObject{}, Sequences: map[string]uint64{}}
	content, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("filedb: read %q: %w", path, err)
	}
	if err == nil {
		var document map[string]any
		if err = codec.Unmarshal(content, &document); err != nil {
			return nil, fmt.Errorf("filedb: decode %q: %w", path, err)
		}
		if err = decode(schema, document, snapshot); err != nil {
			return nil, fmt.Errorf("filedb: decode %q: %w", path, err)
		}
		changed := false
		for _, table := range schema.Tables {
			for _, row := range snapshot.Tables[table.Name] {
				for _, col := range table.Columns {
					if row[col.Name] == nil {
						row[col.Name] = driver.DefaultValue(col.Type)
						changed = true
					}
				}
			}
		}
		if changed {
			document, err := encode(schema, snapshot)
			if err != nil {
				return nil, err
			}
			data, err := codec.Marshal(document)
			if err != nil {
				return nil, fmt.Errorf("filedb: encode: %w", err)
			}
			if err = writeFile(path, data); err != nil {
				return nil, err
			}
		}
	}
	return mem.NewConnection(schema, snapshot, func(snapshot *mem.Snapshot) error {
		document, err := encode(schema, snapshot)
		if err != nil {
			return err
		}
		data, err := codec.Marshal(document)
		if err != nil {
			return fmt.Errorf("filedb: encode: %w", err)
		}
		return writeFile(path, data)
	})
}

func filePath(connection string) (string, error) {
	u, err := url.Parse(connection)
	if err != nil {
		return "", err
	}
	if u.Host != "" {
		return "", fmt.Errorf("filedb: host %q is not supported", u.Host)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("filedb: query and fragment are not supported")
	}
	p := u.Path
	if u.Opaque != "" {
		p = u.Opaque
	}
	if p == "" {
		return "", errors.New("filedb: file path is required")
	}
	return p, nil
}

func decode(schema *driver.Database, document map[string]any, snapshot *mem.Snapshot) error {
	for name, value := range document {
		table := schema.TableByName(name)
		records, ok := value.([]any)
		if !ok {
			return fmt.Errorf("table %s must be an array", name)
		}
		rows := map[uint64]driver.DataObject{}
		snapshot.Tables[name] = rows
		for _, v := range records {
			record, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("table %s: record must be an object", name)
			}
			id, err := uintValue(record["id"])
			if err != nil || id == 0 {
				return fmt.Errorf("table %s: invalid id %v", name, record["id"])
			}
			if _, exists := rows[id]; exists {
				return fmt.Errorf("table %s: duplicate id %d", name, id)
			}
			row := driver.DataObject{}
			for key, value := range record {
				if key != "id" {
					row[key] = value
				}
			}
			if table != nil {
				for _, col := range table.Columns {
					if v, exists := record[col.Name]; exists {
						v, err = decodeValue(v, col.Type)
						if err != nil {
							return fmt.Errorf("%s.%s: %w", table.Name, col.Name, err)
						}
						row[col.Name] = v
					}
				}
			}
			rows[id] = row
		}
	}
	return nil
}
func uintValue(v any) (uint64, error) { return strconv.ParseUint(fmt.Sprint(v), 10, 64) }
func decodeValue(v any, typ driver.DataType) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch typ {
	case driver.TypeID:
		return uintValue(v)
	case driver.TypeInteger:
		return strconv.ParseInt(fmt.Sprint(v), 10, 64)
	case driver.TypeDecimal:
		if value, ok := v.(string); ok {
			return value, nil
		}
		return fmt.Sprint(v), nil
	case driver.TypeDatetime:
		switch value := v.(type) {
		case string:
			return driver.ParseTime(value)
		case time.Time:
			return value.UTC(), nil
		default:
			return nil, fmt.Errorf("timestamp expected, got %T", v)
		}
	case driver.TypeBinary, driver.TypeFile:
		s, ok := v.(string)
		if !ok {
			return nil, errors.New("binary value must be a base64 string")
		}
		return base64.StdEncoding.DecodeString(s)
	case driver.TypeJSON:
		return v, nil
	case driver.TypeBoolean:
		if _, ok := v.(bool); !ok {
			return nil, errors.New("boolean value expected")
		}
	case driver.TypeString:
		if _, ok := v.(string); !ok {
			return nil, errors.New("string value expected")
		}
	}
	return v, nil
}
func encode(schema *driver.Database, snapshot *mem.Snapshot) (map[string]any, error) {
	doc := map[string]any{}
	for name, rows := range snapshot.Tables {
		table := schema.TableByName(name)
		ids := make([]uint64, 0, len(rows))
		for id := range rows {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if len(ids) == 0 {
			continue
		}
		records := make([]any, 0, len(ids))
		for _, id := range ids {
			row := map[string]any{"id": id}
			for key, value := range rows[id] {
				if key != "id" {
					row[key] = value
				}
			}
			if table != nil {
				for _, col := range table.Columns {
					if v, ok := rows[id][col.Name]; ok {
						if (col.Type == driver.TypeBinary || col.Type == driver.TypeFile) && v != nil {
							switch x := v.(type) {
							case []byte:
								v = base64.StdEncoding.EncodeToString(x)
							case string:
								v = base64.StdEncoding.EncodeToString([]byte(x))
							default:
								return nil, fmt.Errorf("filedb: invalid binary value in %s.%s", table.Name, col.Name)
							}
						}
						row[col.Name] = v
					}
				}
			}
			records = append(records, row)
		}
		doc[name] = records
	}
	return doc, nil
}
func writeFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("filedb: write %q: %w", path, err)
	}
	return nil
}

// Normalize converts YAML maps to maps with string keys recursively.
func Normalize(v any) (any, error) {
	switch x := v.(type) {
	case map[interface{}]interface{}:
		m := map[string]any{}
		for k, v := range x {
			s, ok := k.(string)
			if !ok {
				return nil, errors.New("filedb: non-string object key")
			}
			v, e := Normalize(v)
			if e != nil {
				return nil, e
			}
			m[s] = v
		}
		return m, nil
	case map[string]any:
		m := map[string]any{}
		for k, v := range x {
			v, e := Normalize(v)
			if e != nil {
				return nil, e
			}
			m[k] = v
		}
		return m, nil
	case []any:
		a := make([]any, len(x))
		for i, v := range x {
			v, e := Normalize(v)
			if e != nil {
				return nil, e
			}
			a[i] = v
		}
		return a, nil
	case json.Number:
		return x, nil
	default:
		return v, nil
	}
}
