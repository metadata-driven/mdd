// Package xquery parses the query language described in spec.md.
package xquery

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Args contains named values used while parsing a query.
type Args map[string]string

// Parse parses and validates an xquery string.
//
// The optional args provide defaults for directives omitted from src. Currently,
// only the "from" argument is supported. An explicit from directive takes
// precedence over the argument.
func Parse(src string, args ...Args) (*Query, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("xquery: expected at most one argument set")
	}

	var defaultFrom string
	if len(args) == 1 {
		defaultFrom = args[0]["from"]
	}

	tokens, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := parser{tokens: tokens, defaultFrom: defaultFrom}
	p.reserveQuerySlices()
	return p.parse()
}

func parseFromArg(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	tokens, err := lex(value)
	if err != nil {
		return "", fmt.Errorf("xquery: invalid from argument: %w", err)
	}
	p := parser{tokens: tokens}
	from, err := p.parsePath(tSlash, '/')
	if err != nil || p.peek().kind != tEOF || from != value {
		return "", fmt.Errorf("xquery: invalid from argument %q", value)
	}
	return from, nil
}

type tokenKind uint8

const (
	tEOF tokenKind = iota
	tWord
	tString
	tNumber
	tLParen
	tRParen
	tLBracket
	tRBracket
	tComma
	tDot
	tSlash
	tOperator
)

type token struct {
	kind tokenKind
	text string
	line int32
	col  int32
}

func lex(src string) ([]token, error) {
	n := len(src)
	out := make([]token, 0, min(256, max(16, n/4)))
	line, col := int32(1), int32(1)
	for i := 0; i < n; {
		c := src[i]
		if c == '\n' {
			i++
			line++
			col = 1
			continue
		}
		if isSpace(c) {
			i++
			col++
			continue
		}
		if c == '#' || (c == '/' && i+1 < n && src[i+1] == '/') {
			for i < n && src[i] != '\n' {
				i++
				col++
			}
			continue
		}
		start, startCol := i, col
		if c == '"' {
			i++
			col++
			contentStart := i
			hasEscape := false
			closed := false
			for i < n {
				if src[i] == '\n' {
					return nil, fmt.Errorf("xquery:%d:%d: newline in string", line, startCol)
				}
				if src[i] < 0x20 {
					return nil, fmt.Errorf("xquery:%d:%d: control character in string", line, startCol)
				}
				if src[i] == '\\' {
					hasEscape = true
					if i+1 >= n {
						break
					}
					i += 2
					col += 2
					continue
				}
				if src[i] == '"' {
					contentEnd := i
					i++
					col++
					value := src[contentStart:contentEnd]
					if hasEscape {
						if err := json.Unmarshal([]byte(src[start:i]), &value); err != nil {
							return nil, fmt.Errorf("xquery:%d:%d: invalid string: %w", line, startCol, err)
						}
					}
					out = append(out, token{tString, value, line, startCol})
					closed = true
					break
				}
				i++
				col++
			}
			if !closed {
				return nil, fmt.Errorf("xquery:%d:%d: unterminated string", line, startCol)
			}
			continue
		}
		if isNumberStart(src, i) {
			i++
			col++
			for i < n && isNumberByte(src[i]) {
				i++
				col++
			}
			out = append(out, token{tNumber, src[start:i], line, startCol})
			continue
		}
		if isLetter(c) || c == '_' {
			i++
			col++
			for i < n && (isLetter(src[i]) || isDigit(src[i]) || src[i] == '_' || src[i] == '-') {
				i++
				col++
			}
			out = append(out, token{tWord, src[start:i], line, startCol})
			continue
		}
		if kind := punctuationKind(c); kind != 0 {
			out = append(out, token{kind: kind, line: line, col: col})
			i++
			col++
			continue
		}
		if isOperatorStart(c) {
			i++
			col++
			if i < n && src[i] == '=' {
				i++
				col++
			}
			text := src[start:i]
			if text == "!" {
				return nil, fmt.Errorf("xquery:%d:%d: invalid operator", line, startCol)
			}
			out = append(out, token{tOperator, text, line, startCol})
			continue
		}
		return nil, fmt.Errorf("xquery:%d:%d: unexpected character %q", line, col, c)
	}
	out = append(out, token{kind: tEOF, line: line, col: col})
	return out, nil
}

func isNumberStart(src string, i int) bool {
	return isDigit(src[i]) || src[i] == '-' && i+1 < len(src) && isDigit(src[i+1])
}

func isSpace(c byte) bool  { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isNumberByte(c byte) bool {
	return isDigit(c) || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}
func isOperatorStart(c byte) bool {
	return c == '=' || c == '!' || c == '>' || c == '<' || c == '~' || c == '&'
}

func punctuationKind(c byte) tokenKind {
	switch c {
	case '(':
		return tLParen
	case ')':
		return tRParen
	case '[':
		return tLBracket
	case ']':
		return tRBracket
	case ',':
		return tComma
	case '.':
		return tDot
	case '/':
		return tSlash
	default:
		return tEOF
	}
}

type parser struct {
	tokens      []token
	pos         int
	query       Query
	defaultFrom string
}

func (p *parser) reserveQuerySlices() {
	selectCount, whereCount, orderCount := 0, 0, 0
	for i := 0; i+1 < len(p.tokens); {
		if p.tokens[i].kind != tWord || p.tokens[i+1].kind != tLParen {
			i++
			continue
		}
		end := i + 2
		for end < len(p.tokens) && p.tokens[end].kind != tRParen {
			end++
		}
		switch p.tokens[i].text {
		case "select":
			selectCount += itemCapacity(p.tokens, i+2, end)
		case "where":
			whereCount++
		case "order":
			orderCount += itemCapacity(p.tokens, i+2, end)
		}
		i = end + 1
	}
	if selectCount > 0 {
		p.query.Select = make([]Field, 0, selectCount)
	}
	if whereCount > 0 {
		p.query.Where = make([]Condition, 0, whereCount)
	}
	if orderCount > 0 {
		p.query.Order = make([]Order, 0, orderCount)
	}
}

func itemCapacity(tokens []token, start, end int) int {
	if start >= end {
		return 0
	}
	count := 1
	for i := start; i < end; i++ {
		if tokens[i].kind == tComma {
			count++
		}
	}
	if tokens[end-1].kind == tComma {
		count--
	}
	return count
}

func (p *parser) parse() (*Query, error) {
	for p.peek().kind != tEOF {
		name, err := p.take(tWord, "directive name")
		if err != nil {
			return nil, err
		}
		if _, err = p.take(tLParen, "("); err != nil {
			return nil, err
		}
		switch name.text {
		case "from":
			if p.query.From != "" {
				return nil, p.errorf(name, "from may appear only once")
			}
			p.query.From, err = p.parsePath(tSlash, '/')
		case "select":
			err = p.parseSelect()
		case "where":
			err = p.parseWhere()
		case "order":
			err = p.parseOrder()
		case "limit":
			err = p.parseLimit()
		case "offset":
			err = p.parseOffset()
		default:
			return nil, p.errorf(name, "unknown directive %q", name.text)
		}
		if err != nil {
			return nil, err
		}
		if _, err = p.take(tRParen, ")"); err != nil {
			return nil, err
		}
	}
	if p.query.From == "" {
		var err error
		p.query.From, err = parseFromArg(p.defaultFrom)
		if err != nil {
			return nil, err
		}
	}
	if p.query.From == "" {
		return nil, p.errorf(p.peek(), "from is required in the query or parser arguments")
	}
	return &p.query, nil
}

func (p *parser) parsePath(separator tokenKind, separatorByte byte) (string, error) {
	first, err := p.take(tWord, "name")
	if err != nil {
		return "", err
	}
	if p.peek().kind != separator {
		return first.text, nil
	}
	var path strings.Builder
	path.Grow(len(first.text) + 16)
	path.WriteString(first.text)
	for p.peek().kind == separator {
		p.pos++
		next, err := p.take(tWord, "name")
		if err != nil {
			return "", err
		}
		path.WriteByte(separatorByte)
		path.WriteString(next.text)
	}
	return path.String(), nil
}

func (p *parser) parseField() (Field, error) {
	field, err := p.parsePath(tDot, '.')
	return Field(field), err
}

func (p *parser) parseSelect() error {
	for p.peek().kind != tRParen {
		field, err := p.parseField()
		if err != nil {
			return err
		}
		p.query.Select = append(p.query.Select, field)
		if !p.commaOrEnd() {
			return p.errorf(p.peek(), "expected comma or )")
		}
	}
	return nil
}

func (p *parser) parseWhere() error {
	field, err := p.parseField()
	if err != nil {
		return err
	}
	op, err := p.parseOperator()
	if err != nil {
		return err
	}
	values, err := p.parseValues()
	if err != nil {
		return err
	}
	if len(values) == 0 && op == "&=" {
		return p.errorf(p.peek(), "operator &= requires at least one value")
	}
	if len(values) != 1 && op != "=" && op != "!=" && op != "&=" {
		return p.errorf(p.peek(), "operator %s requires exactly one value", op)
	}
	p.query.Where = append(p.query.Where, Condition{Field: field, Op: op, Values: values})
	return nil
}

func (p *parser) parseOperator() (string, error) {
	t := p.peek()
	txt := t.text
	if t.kind == tOperator {
		p.pos++
		if txt == "&=" || txt == "=" || txt == "!=" || txt == ">" || txt == ">=" || txt == "<" || txt == "<=" || txt == "~" {
			return txt, nil
		}
	}
	if t.kind == tWord && txt == "in" {
		p.pos++
		return "=", nil
	}
	if t.kind == tWord && txt == "not" {
		p.pos++
		next, err := p.take(tWord, "in")
		if err == nil && next.text == "in" {
			return "!=", nil
		}
	}
	return "", p.errorf(t, "expected operator")
}

func (p *parser) parseValues() ([]any, error) {
	if p.peek().kind == tLBracket {
		p.pos++
		values, err := p.parseValueList(tRBracket)
		if err != nil {
			return nil, err
		}
		_, err = p.take(tRBracket, "]")
		return values, err
	}
	return p.parseValueList(tRParen)
}

func (p *parser) parseValueList(end tokenKind) ([]any, error) {
	capacity := p.valueCapacity(end)
	if capacity == 0 {
		return nil, nil
	}
	values := make([]any, 0, capacity)
	for p.peek().kind != end {
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		if nested, ok := value.([]any); ok {
			values = append(values, nested...)
		} else {
			values = append(values, value)
		}
		if !p.commaOr(end) {
			return nil, p.errorf(p.peek(), "expected comma or closing delimiter")
		}
	}
	return values, nil
}

func (p *parser) valueCapacity(end tokenKind) int {
	if p.peek().kind == end {
		return 0
	}
	count := 1
	for i := p.pos; i < len(p.tokens); i++ {
		if p.tokens[i].kind == end {
			return trailingCommaCapacity(p.tokens, i, count)
		}
		if p.tokens[i].kind == tComma {
			count++
		}
	}
	return count
}

func trailingCommaCapacity(tokens []token, end, count int) int {
	if end > 0 && tokens[end-1].kind == tComma {
		return count - 1
	}
	return count
}

func (p *parser) parseValue() (any, error) {
	t := p.peek()
	p.pos++
	switch t.kind {
	case tString:
		return t.text, nil
	case tWord:
		switch t.text {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		}
		return t.text, nil
	case tNumber:
		if !strings.ContainsAny(t.text, ".eE") {
			if n, err := strconv.ParseInt(t.text, 10, 64); err == nil {
				return n, nil
			}
		}
		n, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, p.errorf(t, "invalid number")
		}
		return n, nil
	case tLBracket:
		values, err := p.parseValueList(tRBracket)
		if err != nil {
			return nil, err
		}
		if _, err = p.take(tRBracket, "]"); err != nil {
			return nil, err
		}
		return values, nil
	default:
		return nil, p.errorf(t, "expected value")
	}
}

func (p *parser) parseOrder() error {
	for p.peek().kind != tRParen {
		field, err := p.parseField()
		if err != nil {
			return err
		}
		item := Order{Field: field}
		if p.peek().kind == tWord && (p.peek().text == "asc" || p.peek().text == "desc") {
			item.Desc = p.peek().text == "desc"
			p.pos++
		}
		p.query.Order = append(p.query.Order, item)
		if !p.commaOrEnd() {
			return p.errorf(p.peek(), "expected comma or )")
		}
	}
	return nil
}

func (p *parser) parseLimit() error {
	if p.query.Limit != 0 {
		return p.errorf(p.peek(), "limit may appear only once")
	}
	t, err := p.take(tNumber, "integer")
	if err != nil {
		return err
	}
	n, err := strconv.ParseInt(t.text, 10, 64)
	if err != nil || n <= 0 {
		return p.errorf(t, "limit must be a positive integer")
	}
	p.query.Limit = n
	return nil
}

func (p *parser) parseOffset() error {
	if p.query.Offset != nil {
		return p.errorf(p.peek(), "offset may appear only once")
	}
	t := p.peek()
	if t.kind == tString {
		p.pos++
		p.query.Offset = t.text
		return nil
	}
	if t.kind != tNumber {
		return p.errorf(t, "offset must be a non-negative integer or string")
	}
	p.pos++
	n, err := strconv.ParseInt(t.text, 10, 64)
	if err != nil || n < 0 {
		return p.errorf(t, "offset must be a non-negative integer or string")
	}
	p.query.Offset = n
	return nil
}

func (p *parser) commaOrEnd() bool { return p.commaOr(tRParen) }

func (p *parser) commaOr(end tokenKind) bool {
	if p.peek().kind == end {
		return true
	}
	if p.peek().kind != tComma {
		return false
	}
	p.pos++
	return true
}

func (p *parser) peek() token { return p.tokens[p.pos] }

func (p *parser) take(kind tokenKind, expected string) (token, error) {
	t := p.peek()
	if t.kind != kind {
		return t, p.errorf(t, "expected %s", expected)
	}
	p.pos++
	return t, nil
}

func (p *parser) errorf(t token, format string, args ...any) error {
	return fmt.Errorf("xquery:%d:%d: %s", t.line, t.col, fmt.Sprintf(format, args...))
}
