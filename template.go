package mdd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/goldic/xtpl"
	"github.com/metadata-driven/mdd/driver"
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
	template, err := parseTemplate(c.parser, expr)
	if err != nil {
		return nil, err
	}
	value, _ := c.values.LoadOrStore(expr, template)
	return value.(Template), nil
}

func parseTemplate(parser TemplateParser, expr string) (Template, error) {
	if parser == nil {
		return nil, fmt.Errorf("template parser is nil")
	}
	template, err := parser(expr)
	if err != nil {
		return nil, err
	}
	if template == nil {
		return nil, fmt.Errorf("template parser returned nil")
	}
	return template, nil
}

func (s *schema) warmTemplates() error {
	textParser := TemplateEngine.Text
	text := make(map[string]Template)
	query := &templateCache{parser: TemplateEngine.Query}
	for _, model := range s.config.Models() {
		table := s.tables[model.Name()]
		toTitleExpr := model.ToTitle()
		if toTitleExpr == "" {
			toTitleExpr = "{UID}"
			if table.hasAttr("title", driver.TypeString) {
				toTitleExpr = "{title}"
			} else if table.hasAttr("name", driver.TypeString) {
				toTitleExpr = "{name}"
			}
		}
		for _, field := range []struct {
			name  string
			value string
			dst   *Template
		}{
			{"to_title", toTitleExpr, &table.title},
			{"to_description", model.ToDescription(), &table.description},
			{"to_icon", model.ToIcon(), &table.icon},
		} {
			if field.value == "" {
				continue
			}
			template := text[field.value]
			if template == nil {
				var err error
				template, err = parseTemplate(textParser, field.value)
				if err != nil {
					return fmt.Errorf("%s.%s: parse template: %w", model.Path(), field.name, err)
				}
				text[field.value] = template
			}
			*field.dst = template
		}
		for _, attr := range model.Attrs() {
			for _, field := range []struct{ name, value string }{
				{"query", attr.Query()},
				{"lookup", attr.LookupQuery()},
			} {
				if field.value == "" {
					continue
				}
				if _, err := query.parse(field.value); err != nil {
					return fmt.Errorf("%s.%s: parse template: %w", attr.Path(), field.name, err)
				}
			}
		}
	}
	s.queryTemplates = query
	return nil
}
