// Package modelgen updates Go model packages from their MDD metadata.
package modelgen

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/metadata-driven/mdd/meta"
)

type generator struct {
	pkg     *sourcePackage
	names   map[string]string // Metadata path to Go type name.
	claimed map[symbol]string
}

type member struct {
	key    symbol
	origin string
	call   runtimeCall
	code   func(*sourceFile) string
}

// Generate reads meta.yaml in dir and updates the Go package in place. Only
// declarations marked with "// mdd:generated" are eligible for replacement or removal.
func Generate(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	filename := filepath.Join(dir, "meta.yaml")
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	cfg, err := meta.ParseYAML(data)
	if err != nil {
		return fmt.Errorf("%s: %w", filename, err)
	}
	pkg, err := readPackage(dir)
	if err != nil {
		return err
	}
	gen := generator{pkg: pkg, names: make(map[string]string), claimed: make(map[symbol]string)}
	if err = gen.plan(cfg); err != nil {
		return err
	}
	gen.prune(cfg)
	// Resolve and format all edits before writing any file.
	output := make([][]byte, len(pkg.files))
	for i, file := range pkg.files {
		output[i], err = file.render()
		if err != nil {
			return err
		}
	}
	for i, file := range pkg.files {
		if output[i] != nil {
			if err := file.write(output[i]); err != nil {
				return fmt.Errorf("write %s: %w", file.path, err)
			}
		}
	}
	return nil
}

func (g *generator) prune(cfg meta.Config) {
	for _, file := range g.pkg.files {
		if file.constrained {
			continue
		}
		for _, d := range file.decls {
			if g.claimed[d.key] != "" || !d.managed() || !d.obsolete(cfg) {
				continue
			}
			start := file.offset(d.start())
			lineStart := strings.LastIndexByte(file.source[:start], '\n') + 1
			if lineStart > 0 && strings.TrimSpace(file.source[lineStart:start]) == "" {
				// Consume the preceding line break so insertions at the end still work.
				start = lineStart - 1
				if start > 0 && file.source[start-1] == '\r' {
					start--
				}
			}
			end, _ := file.lineEnd(d.node.End())
			file.edits = append(file.edits, edit{start: start, end: end})
		}
	}
}

func (d *declaration) obsolete(cfg meta.Config) bool {
	switch d.kind {
	case token.TYPE:
		return embeds(d, "EnumItem")
	case token.FUNC:
		fn := d.node.(*ast.FuncDecl)
		if d.key.receiver == "" || fn.Body == nil || len(fn.Body.List) != 1 {
			return false
		}
		ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return false
		}
		call, ok := ast.Unparen(ret.Results[0]).(*ast.CallExpr)
		if !ok {
			return false
		}
		switch d.file.runtimeName(call.Fun) {
		case "Attr", "AttrSet", "SetAttr", "ID", "Ctx", "NewSet":
			return true
		}
	case token.VAR:
		switch d.call.name {
		case "Item":
			node := cfg.Lookup(d.call.path)
			return node == nil || node.Kind() != meta.KindItem || node.Path() != d.call.path
		case "RegisterEnum":
			return cfg.LookupEnum(d.call.path) == nil
		case "RegisterModel":
			return cfg.LookupModel(d.call.path) == nil
		}
	}
	return false
}

func (g *generator) group(file *sourceFile, kind token.Token, block bool, matches func(*declaration) bool, members ...member) error {
	var missing []member
	for _, m := range members {
		if m.key.name != "_" {
			if !token.IsIdentifier(m.key.name) {
				return fmt.Errorf("%s cannot be represented as the Go identifier %q", m.origin, m.key.name)
			}
			if prior, ok := g.claimed[m.key]; ok {
				return fmt.Errorf("%s and %s both generate %s", prior, m.origin, m.key.name)
			}
			g.claimed[m.key] = m.origin
		}
		var found []*declaration
		if m.call.name != "" {
			found = g.pkg.calls[m.call]
		}
		if len(found) == 0 && m.key.name != "_" {
			found = g.pkg.symbols[m.key]
		}
		switch len(found) {
		case 0:
			if !g.pkg.commented[m.key] {
				missing = append(missing, m)
			}
		case 1:
			d := found[0]
			if d.kind != kind || (m.call.name != "" && d.call != m.call && !d.managed()) {
				return fmt.Errorf("%s: %s conflicts with the declaration for %s", d.file.path, d.key.name, m.origin)
			}
			if d.file.constrained {
				return fmt.Errorf("%s: %s for %s is in a build-constrained file", d.file.path, d.key.name, m.origin)
			}
			if d.managed() && d.key == m.key {
				code := m.code(d.file)
				if d.blockSpec() {
					code = strings.TrimPrefix(code, kind.String()+" ")
				}
				end, _ := d.file.lineEnd(d.node.End())
				d.file.edits = append(d.file.edits, edit{d.file.offset(d.start()), end, code + " " + marker})
			}
		default:
			return fmt.Errorf("%s: multiple declarations found for %s", m.origin, m.key.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	anchor := g.pkg.groupEnd(file, matches)
	pos, spacing, inBlock := len(file.source), "\n\n", false
	if anchor != nil {
		file = anchor.file
		pos = len(file.source)
		if anchor.blockSpec() && anchor.kind == kind {
			if end, ok := file.lineEnd(anchor.node.End()); ok {
				pos, spacing, inBlock = end, "\n", true
			}
		}
		if !inBlock {
			node := anchor.node
			if anchor.group != nil {
				node = anchor.group
			}
			if end, ok := file.lineEnd(node.End()); ok {
				pos = end
				if !anchor.blockSpec() && anchor.kind == kind {
					spacing = "\n"
				}
			}
		}
	}
	var lines []string
	for _, m := range missing {
		code := m.code(file)
		if inBlock || block && anchor == nil {
			code = "\t" + strings.TrimPrefix(code, kind.String()+" ")
		}
		lines = append(lines, code+" "+marker)
	}
	code := strings.Join(lines, "\n")
	if block && anchor == nil {
		code = kind.String() + " (\n" + code + "\n)"
	}
	file.edits = append(file.edits, edit{pos, pos, spacing + code + "\n"})
	return nil
}

func (pkg *sourcePackage) groupEnd(preferred *sourceFile, matches func(*declaration) bool) *declaration {
	if matches == nil {
		return nil
	}
	for _, file := range append([]*sourceFile{preferred}, pkg.files...) {
		if file.constrained {
			continue
		}
		var last *declaration
		for _, d := range file.decls {
			if !matches(d) {
				if last != nil && last.node != d.node {
					break
				}
				continue
			}
			if last != nil {
				end := file.positions.PositionFor(last.node.End(), false).Line
				start := file.positions.PositionFor(d.start(), false).Line
				if start > end+1 || (last.blockSpec() || d.blockSpec()) && last.group != d.group {
					break
				}
			}
			last = d
		}
		if last != nil {
			return last
		}
	}
	return nil
}

func typeMember(name, origin, embedded string) member {
	return member{key: symbol{name: name}, origin: origin, code: func(f *sourceFile) string {
		return fmt.Sprintf("type %s struct{ %s }", name, f.qualify(runtimeImport, embedded))
	}}
}

func methodMember(receiver, name, origin string, code func(*sourceFile) string) member {
	return member{key: symbol{receiver: receiver, name: name}, origin: origin, code: code}
}

func bindingMember(name, target, fn, path string) member {
	call := runtimeCall{name: fn, path: path}
	return member{key: symbol{name: name}, origin: path, call: call, code: func(f *sourceFile) string {
		return fmt.Sprintf("var %s = %s[%s](Meta, %q)", name, f.qualify(runtimeImport, fn), target, path)
	}}
}

func embeds(d *declaration, name string) bool {
	spec, ok := d.node.(*ast.TypeSpec)
	if !ok {
		return false
	}
	typ, ok := spec.Type.(*ast.StructType)
	if !ok {
		return false
	}
	for _, field := range typ.Fields.List {
		if len(field.Names) == 0 && d.file.runtimeName(field.Type) == name {
			return true
		}
	}
	return false
}
