package mdd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/goldic/xtpl"
	"github.com/metadata-driven/mdd/meta"
)

// Template is a compiled expression that can be evaluated against a value.
type Template interface {
	Execute(io.Writer, any) error
}

// TemplateParser compiles a template expression.
type TemplateParser func(string) (Template, error)

// TemplateEngine holds the parsers used when an application opens.
var TemplateEngine = struct {
	Text  TemplateParser
	Query TemplateParser
}{
	Text: func(expr string) (Template, error) {
		return xtpl.ParseTemplate(expr, "{", "}", nil)
	},
	Query: func(expr string) (Template, error) {
		return xtpl.ParseTemplate(expr, "{", "}", func(value any) io.Reader {
			if xtpl.IsNil(value) {
				value = nil
			}
			data, _ := json.Marshal(value)
			return bytes.NewReader(data)
		})
	},
}

type templateCache struct {
	parser TemplateParser
	values sync.Map
}

func (c *templateCache) parse(expr string) (Template, error) {
	if value, ok := c.values.Load(expr); ok {
		return value.(Template), nil
	}
	if c.parser == nil {
		return nil, fmt.Errorf("template parser is nil")
	}
	template, err := c.parser(expr)
	if err != nil {
		return nil, err
	}
	if template == nil {
		return nil, fmt.Errorf("template parser returned nil")
	}
	value, _ := c.values.LoadOrStore(expr, template)
	return value.(Template), nil
}

func (s *schema) warmTemplates(root meta.Node) error {
	var visit func(meta.Node) error
	visit = func(node meta.Node) error {
		var title, description, icon Template
		for _, field := range []struct {
			name  string
			value string
			dst   *Template
		}{
			{"title", node.Title(), &title},
			{"description", node.Description(), &description},
			{"icon", node.Icon(), &icon},
		} {
			if field.value == "" {
				continue
			}
			template, err := s.textTemplates.parse(field.value)
			if err != nil {
				return fmt.Errorf("%s.%s: parse template: %w", node.Path(), field.name, err)
			}
			*field.dst = template
		}
		if node.Kind() == meta.KindModel {
			if table := s.tables[node.Path()]; table != nil {
				table.title, table.description, table.icon = title, description, icon
			}
		}
		if attr, ok := node.(meta.Attribute); ok {
			for _, field := range []struct{ name, value string }{
				{"query", attr.Query()},
				{"lookup", attr.LookupQuery()},
			} {
				if field.value == "" {
					continue
				}
				if _, err := s.queryTemplates.parse(field.value); err != nil {
					return fmt.Errorf("%s.%s: parse template: %w", node.Path(), field.name, err)
				}
			}
		}
		for _, child := range node.Children() {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(root)
}
