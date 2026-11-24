package meta

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/goldic/js"
	"github.com/goldic/try"
	"gopkg.in/yaml.v2"
)

// CurrentVersion is the metadata format version used when version is omitted.
const CurrentVersion = "1.0"

var currentMajorVersion, _ = parseVersion(CurrentVersion)

// ParseYAML checks the document version and warms all metadata accessors.
func ParseYAML(data []byte) (config Config, err error) {
	defer try.Catch(&err)
	var value any
	if err = yaml.UnmarshalStrict(data, &value); err != nil {
		return nil, fmt.Errorf("parse metadata YAML: %w", err)
	}
	raw := stringMap(value, "root")
	version, err := normalizeVersion(raw, data)
	if err != nil {
		return nil, fmt.Errorf("parse metadata YAML: %w", err)
	}
	if major, _ := parseVersion(version); major != currentMajorVersion {
		panic(fmt.Errorf("unsupported metadata version %v: this parser supports %d.x", version, currentMajorVersion))
	}

	config = newRootNode(raw)
	return
}

// MustParseYAML parses metadata or panics if it is invalid.
func MustParseYAML(data []byte) Config {
	config, err := ParseYAML(data)
	if err != nil {
		panic(err)
	}
	return config
}

// normalize only converts YAML containers; it never rewrites metadata values.
func normalize(value any) any {
	switch value := value.(type) {
	case map[any]any:
		result := make(js.Object, len(value))
		for key, item := range value {
			name, ok := key.(string)
			if !ok {
				panic(fmt.Errorf("metadata mapping keys must be strings"))
			}
			result[name] = normalize(item)
		}
		return result
	case []any:
		for i, item := range value {
			value[i] = normalize(item)
		}
	}
	return value
}

func stringMap(value any, location string) js.Object {
	result, ok := normalize(value).(js.Object)
	if !ok {
		panic(fmt.Errorf("%s must be an object", location))
	}
	return result
}

func optionalMap(parent js.Object, key, location string) js.Object {
	if parent[key] == nil {
		return nil
	}
	return stringMap(parent[key], location)
}

func objectList(parent js.Object, key, location string) []js.Object {
	if parent[key] == nil {
		return nil
	}
	list, ok := parent[key].([]any)
	if !ok {
		panic(fmt.Errorf("%s must be a list", location))
	}
	result := make([]js.Object, len(list))
	for i, item := range list {
		result[i] = stringMap(item, fmt.Sprintf("%s[%d]", location, i))
	}
	return result
}

func optionalString(parent js.Object, key, location string) string {
	if parent[key] == nil {
		return ""
	}
	return scalarString(parent[key], location)
}

func scalarString(value any, location string) string {
	result, ok := value.(string)
	if !ok {
		panic(fmt.Errorf("%s must be a string", location))
	}
	return result
}

func parseIndexType(value any, location string) IndexType {
	indexType := IndexType(scalarString(value, location))
	switch indexType {
	case IndexTypeRegular, IndexTypeUnique, IndexTypeFulltext:
		return indexType
	default:
		panic(fmt.Errorf("%s has unknown index type %q", location, indexType))
	}
}

func stringList(value any, location string) []string {
	if value == nil {
		return nil
	}
	values, ok := value.([]any)
	if !ok {
		panic(fmt.Errorf("%s must be a list of strings", location))
	}
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = scalarString(value, fmt.Sprintf("%s[%d]", location, i))
		if result[i] == "" {
			panic(fmt.Errorf("%s[%d] must not be empty", location, i))
		}
	}
	return result
}

func normalizeVersion(raw js.Object, orgYAML []byte) (version string, err error) {
	if !raw.Has("version") {
		raw.Set("version", CurrentVersion)
	}
	if _, ok := raw.Get("version").Any().(string); !ok { // is not string get raw version
		var obj struct {
			RawVersion string `yaml:"version"`
		}
		if err = yaml.Unmarshal(orgYAML, &obj); err != nil { // not strict Unmarshal
			return "", err
		}
		raw.Set("version", obj.RawVersion)
	}
	version = strings.TrimSpace(raw.GetStr("version"))
	if !strings.ContainsRune(version, '.') {
		version += ".0"
	}
	raw.Set("version", version) // set normalized version
	return
}

var versionPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)$`)

func parseVersion(version string) (major, minor int) {
	parts := versionPattern.FindStringSubmatch(version)
	if parts == nil {
		panic(fmt.Errorf("metadata version must be in major.minor format"))
	}
	major = js.ToInt(strings.TrimLeft(parts[1], "0"))
	minor = js.ToInt(strings.TrimLeft(parts[2], "0"))
	return
}
