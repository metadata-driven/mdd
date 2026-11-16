// Package yaml registers YAML file-backed storage drivers.
package yaml

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/driver/json/filedb"
	"gopkg.in/yaml.v3"
)

var _ = driver.Register(open, "yaml", "yml")

func open(schema *driver.Database) (driver.Connection, error) {
	return filedb.Open(schema, filedb.Codec{
		Marshal: func(value any) ([]byte, error) {
			return yaml.Marshal(yamlNumbers(value))
		},
		Unmarshal: func(data []byte, out any) error {
			decoder := yaml.NewDecoder(bytes.NewReader(data))
			var document yaml.Node
			if err := decoder.Decode(&document); err != nil && err != io.EOF {
				return err
			}
			var extra yaml.Node
			if err := decoder.Decode(&extra); err != io.EOF {
				if err == nil {
					return fmt.Errorf("multiple YAML documents")
				}
				return err
			}
			if len(document.Content) == 0 {
				*out.(*map[string]any) = map[string]any{}
				return nil
			}
			value, err := yamlValue(document.Content[0], make(map[*yaml.Node]bool))
			if err != nil {
				return err
			}
			if value == nil {
				*out.(*map[string]any) = map[string]any{}
				return nil
			}
			result, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("yaml: database document must be an object")
			}
			*out.(*map[string]any) = result
			return nil
		},
	})
}

type yamlNumber string

func (n yamlNumber) MarshalYAML() (any, error) {
	value := string(n)
	if _, err := json.Marshal(json.Number(value)); err != nil {
		return nil, err
	}
	tag := "!!int"
	if strings.ContainsAny(value, ".eE") {
		tag = "!!float"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}, nil
}

func yamlNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		return yamlNumber(v)
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, item := range v {
			result[key] = yamlNumbers(item)
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = yamlNumbers(item)
		}
		return result
	default:
		return value
	}
}

// yamlValue preserves the original text of JSON-compatible numeric scalars.
func yamlValue(node *yaml.Node, visiting map[*yaml.Node]bool) (any, error) {
	if visiting[node] {
		return nil, fmt.Errorf("yaml: cyclic alias")
	}
	visiting[node] = true
	defer delete(visiting, node)
	switch node.Kind {
	case yaml.AliasNode:
		return yamlValue(node.Alias, visiting)
	case yaml.SequenceNode:
		result := make([]any, len(node.Content))
		for i, item := range node.Content {
			value, err := yamlValue(item, visiting)
			if err != nil {
				return nil, err
			}
			result[i] = value
		}
		return result, nil
	case yaml.MappingNode:
		result := make(map[string]any, len(node.Content)/2)
		// Merge aliases first; explicitly declared keys take precedence.
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Tag != "!!merge" {
				continue
			}
			merged, err := yamlValue(node.Content[i+1], visiting)
			if err != nil {
				return nil, err
			}
			maps, ok := merged.([]any)
			if !ok {
				maps = []any{merged}
			}
			for _, item := range maps {
				m, ok := item.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("yaml: merge value must be an object")
				}
				for key, value := range m {
					if _, exists := result[key]; !exists {
						result[key] = value
					}
				}
			}
		}
		seen := make(map[string]bool, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			if keyNode.Tag == "!!merge" {
				continue
			}
			keyValue, err := yamlValue(keyNode, visiting)
			if err != nil {
				return nil, err
			}
			key, ok := keyValue.(string)
			if !ok {
				return nil, fmt.Errorf("yaml: non-string object key")
			}
			if seen[key] {
				return nil, fmt.Errorf("yaml: duplicate key %q", key)
			}
			seen[key] = true
			value, err := yamlValue(node.Content[i+1], visiting)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		return result, nil
	case yaml.ScalarNode:
		if (node.Tag == "!!int" || node.Tag == "!!float") && json.Valid([]byte(node.Value)) {
			return json.Number(node.Value), nil
		}
		var value any
		if err := node.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	default:
		return nil, fmt.Errorf("yaml: unsupported node kind %d", node.Kind)
	}
}
