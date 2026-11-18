package modelgen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/build/constraint"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const marker = "// mdd:generated"
const runtimeImport = "github.com/metadata-driven/mdd"

type symbol struct {
	receiver string
	name     string
}

type runtimeCall struct {
	name string
	path string
}

type declaration struct {
	file   *sourceFile
	key    symbol
	node   ast.Node
	group  *ast.GenDecl
	kind   token.Token
	call   runtimeCall
	target string // Type argument of a runtime call.
}

type edit struct {
	start, end int
	text       string
}

type sourceFile struct {
	path        string
	original    []byte
	mode        os.FileMode
	source      string
	ast         *ast.File
	positions   *token.FileSet
	decls       []*declaration
	imports     map[string]*ast.ImportSpec
	newImports  map[string]string
	edits       []edit
	constrained bool
}

type sourcePackage struct {
	dir       string
	name      string
	files     []*sourceFile
	byName    map[string]*sourceFile
	symbols   map[symbol][]*declaration
	commented map[symbol]bool
	calls     map[runtimeCall][]*declaration
}

func readPackage(dir string) (*sourcePackage, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	pkg := &sourcePackage{
		dir: dir, byName: make(map[string]*sourceFile), symbols: make(map[symbol][]*declaration),
		commented: make(map[symbol]bool), calls: make(map[runtimeCall][]*declaration),
	}
	var constrained []*sourceFile
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
			!strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular Go file", filepath.Join(dir, name))
		}
		filename := filepath.Join(dir, name)
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		file, err := parseSource(filename, string(data))
		if err != nil {
			return nil, err
		}
		file.original, file.mode = data, info.Mode().Perm()
		if file.constrained {
			pkg.byName[name] = file
			constrained = append(constrained, file)
			continue
		}
		if pkg.name == "" {
			pkg.name = file.ast.Name.Name
		} else if pkg.name != file.ast.Name.Name {
			return nil, fmt.Errorf("%s declares package %s, expected %s", filename, file.ast.Name.Name, pkg.name)
		}
		pkg.addFile(name, file)
	}
	if pkg.name == "" {
		pkg.name = packageName(filepath.Base(dir))
	}
	for _, file := range constrained {
		if file.ast.Name.Name == pkg.name {
			pkg.addFile(filepath.Base(file.path), file)
		}
	}
	return pkg, nil
}

func packageName(dir string) string {
	name := strings.Map(func(r rune) rune {
		if 'a' <= r && r <= 'z' || '0' <= r && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, strings.ToLower(dir))
	if !token.IsIdentifier(name) || name == "_" {
		return "models"
	}
	return name
}

func parseSource(filename, source string) (*sourceFile, error) {
	positions := token.NewFileSet()
	parsed, err := parser.ParseFile(positions, filename, source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	file := &sourceFile{
		path: filename, source: source, ast: parsed, positions: positions,
		imports: make(map[string]*ast.ImportSpec), newImports: make(map[string]string),
		constrained: constrainedFilename(filename),
	}
	for _, group := range parsed.Comments {
		if group.Pos() >= parsed.Package {
			break
		}
		for _, comment := range group.List {
			if constraint.IsGoBuild(comment.Text) || constraint.IsPlusBuild(comment.Text) {
				file.constrained = true
			}
		}
	}
	for _, spec := range parsed.Imports {
		importPath, _ := strconv.Unquote(spec.Path.Value)
		file.imports[importPath] = spec
	}
	return file, nil
}

// Only check the filename. No real GOOS or GOARCH matches these placeholders.
var unconstrained = build.Context{
	GOOS: "modelgen", GOARCH: "modelgen",
	OpenFile: func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("")), nil
	},
}

func constrainedFilename(filename string) bool {
	matched, _ := unconstrained.MatchFile("", filepath.Base(filename))
	return !matched
}

func (pkg *sourcePackage) addFile(name string, file *sourceFile) {
	pkg.files = append(pkg.files, file)
	pkg.byName[name] = file
	for _, node := range file.ast.Decls {
		switch decl := node.(type) {
		case *ast.FuncDecl:
			key := functionKey(decl)
			pkg.addDecl(&declaration{file: file, key: key, node: decl, kind: token.FUNC})
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					pkg.addDecl(&declaration{file: file, key: symbol{name: spec.Name.Name}, node: spec, group: decl, kind: decl.Tok})
				case *ast.ValueSpec:
					for i, name := range spec.Names {
						d := &declaration{file: file, key: symbol{name: name.Name}, node: spec, group: decl, kind: decl.Tok}
						if len(spec.Names) == len(spec.Values) {
							d.call, d.target = file.runtimeBinding(spec.Values[i])
						}
						pkg.addDecl(d)
					}
				}
			}
		}
	}
	file.disabledMethods(pkg.commented)
}

func (pkg *sourcePackage) addDecl(decl *declaration) {
	decl.file.decls = append(decl.file.decls, decl)
	if decl.key.name != "_" {
		pkg.symbols[decl.key] = append(pkg.symbols[decl.key], decl)
	}
	if decl.call.name != "" {
		pkg.calls[decl.call] = append(pkg.calls[decl.call], decl)
	}
}

func functionKey(decl *ast.FuncDecl) symbol {
	key := symbol{name: decl.Name.Name}
	if decl.Recv != nil && len(decl.Recv.List) == 1 {
		key.receiver = baseType(decl.Recv.List[0].Type)
	}
	return key
}

func baseType(expr ast.Expr) string {
	switch expr := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.StarExpr:
		return baseType(expr.X)
	case *ast.IndexExpr:
		return baseType(expr.X)
	case *ast.IndexListExpr:
		return baseType(expr.X)
	}
	return ""
}

var commentedFunc = regexp.MustCompile(`(?m)^[\t ]*func[\t \r\n]*\(`)

func (f *sourceFile) disabledMethods(methods map[symbol]bool) {
	for _, group := range f.ast.Comments {
		var body strings.Builder
		for _, comment := range group.List {
			text := strings.TrimPrefix(comment.Text, "//")
			if strings.HasPrefix(text, "/*") {
				text = strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
			}
			body.WriteString(text)
			body.WriteByte('\n')
		}
		text := body.String()
		starts := commentedFunc.FindAllStringIndex(text, -1)
		for i, start := range starts {
			end := len(text)
			if i+1 < len(starts) {
				end = starts[i+1][0]
			}
			parsed, _ := parser.ParseFile(token.NewFileSet(), "", "package p\n"+text[start[0]:end], parser.SkipObjectResolution)
			if parsed != nil && len(parsed.Decls) != 0 {
				if decl, ok := parsed.Decls[0].(*ast.FuncDecl); ok {
					key := functionKey(decl)
					if key.receiver != "" && key.name != "_" {
						methods[key] = true
					}
				}
			}
		}
	}
}

func (f *sourceFile) runtimeBinding(expr ast.Expr) (runtimeCall, string) {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok || len(call.Args) != 2 {
		return runtimeCall{}, ""
	}
	fn, ok := call.Fun.(*ast.IndexExpr)
	if !ok {
		return runtimeCall{}, ""
	}
	target, ok := fn.Index.(*ast.Ident)
	if !ok {
		return runtimeCall{}, ""
	}
	name := f.runtimeName(fn.X)
	if name != "RegisterModel" && name != "RegisterEnum" && name != "Item" {
		return runtimeCall{}, ""
	}
	literal, ok := call.Args[1].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return runtimeCall{}, ""
	}
	value, _ := strconv.Unquote(literal.Value)
	return runtimeCall{name: name, path: value}, target.Name
}

func (f *sourceFile) runtimeName(expr ast.Expr) string {
	spec := f.imports[runtimeImport]
	if spec == nil {
		return ""
	}
	alias := importName(runtimeImport, spec)
	switch expr := ast.Unparen(expr).(type) {
	case *ast.IndexExpr:
		return f.runtimeName(expr.X)
	case *ast.IndexListExpr:
		return f.runtimeName(expr.X)
	case *ast.Ident:
		if alias == "." {
			return expr.Name
		}
	case *ast.SelectorExpr:
		if x, ok := expr.X.(*ast.Ident); ok && x.Name == alias {
			return expr.Sel.Name
		}
	}
	return ""
}

func importName(importPath string, spec *ast.ImportSpec) string {
	if spec.Name != nil {
		return spec.Name.Name
	}
	return path.Base(importPath)
}

func (pkg *sourcePackage) file(name string) (*sourceFile, error) {
	if file := pkg.byName[name]; file != nil {
		if !file.constrained {
			return file, nil
		}
		return pkg.file(strings.TrimSuffix(name, ".go") + "_mdd.go")
	}
	filename := filepath.Join(pkg.dir, name)
	file, err := parseSource(filename, "package "+pkg.name+"\n")
	if err != nil {
		return nil, err
	}
	file.mode = 0644
	pkg.addFile(name, file)
	return file, nil
}

func (f *sourceFile) qualify(importPath, name string) string {
	if alias, ok := f.newImports[importPath]; ok {
		return alias + "." + name
	}
	spec := f.imports[importPath]
	if spec != nil {
		alias := importName(importPath, spec)
		if alias == "." {
			return name
		}
		if alias != "_" {
			return alias + "." + name
		}
	}
	base := path.Base(importPath)
	alias := base
	for i := 2; f.hasLocalName(alias); i++ {
		alias = base + strconv.Itoa(i)
	}
	if spec != nil {
		f.edits = append(f.edits, edit{f.offset(spec.Name.Pos()), f.offset(spec.Name.End()), alias})
		// Remember the effective alias without inserting a second import.
		f.imports[importPath] = &ast.ImportSpec{Name: ast.NewIdent(alias), Path: spec.Path}
	} else {
		f.newImports[importPath] = alias
	}
	return alias + "." + name
}

func (f *sourceFile) hasImportName(name string) bool {
	for importPath, spec := range f.imports {
		if importName(importPath, spec) == name {
			return true
		}
	}
	for _, alias := range f.newImports {
		if alias == name {
			return true
		}
	}
	return false
}

func (f *sourceFile) hasLocalName(name string) bool {
	return f.hasImportName(name) || slices.ContainsFunc(f.decls, func(d *declaration) bool {
		return d.key == (symbol{name: name})
	})
}

func (f *sourceFile) localName(name string) string {
	for f.hasLocalName(name) {
		name += "_"
	}
	return name
}

func (f *sourceFile) offset(pos token.Pos) int { return f.positions.PositionFor(pos, false).Offset }

// lineEnd finds a safe insertion point after the node and any line comment.
func (f *sourceFile) lineEnd(pos token.Pos) (int, bool) {
	end := f.offset(pos)
	lineEnd := len(f.source)
	if n := strings.IndexByte(f.source[end:], '\n'); n >= 0 {
		lineEnd = end + n
		if lineEnd > end && f.source[lineEnd-1] == '\r' {
			lineEnd--
		}
	}
	tail := strings.TrimSpace(f.source[end:lineEnd])
	return lineEnd, tail == "" || strings.HasPrefix(tail, "//")
}

func (d *declaration) managed() bool {
	if spec, ok := d.node.(*ast.ValueSpec); ok && len(spec.Names) != 1 {
		return false
	}
	end, safe := d.file.lineEnd(d.node.End())
	return safe && strings.TrimSpace(d.file.source[d.file.offset(d.node.End()):end]) == marker
}

func (d *declaration) blockSpec() bool { return d.group != nil && d.group.Lparen.IsValid() }

func (d *declaration) start() token.Pos {
	if d.group != nil && !d.blockSpec() {
		return d.group.Pos()
	}
	return d.node.Pos()
}

func (f *sourceFile) render() ([]byte, error) {
	if len(f.edits) == 0 {
		return nil, nil
	}
	if len(f.newImports) != 0 {
		f.insertImports()
	}
	result, err := applyEdits(f.source, f.edits)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.path, err)
	}
	data, err := format.Source([]byte(result))
	if err != nil {
		return nil, fmt.Errorf("format %s: %w", f.path, err)
	}
	data, err = cleanEditedSource(f.path, data)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(data, f.original) {
		return nil, nil
	}
	return data, nil
}

func applyEdits(source string, edits []edit) (string, error) {
	slices.SortStableFunc(edits, func(a, b edit) int { return a.start - b.start })
	var result strings.Builder
	end := 0
	for _, edit := range edits {
		if edit.start < end {
			return "", fmt.Errorf("overlapping Go declarations")
		}
		result.WriteString(source[end:edit.start])
		result.WriteString(edit.text)
		end = edit.end
	}
	result.WriteString(source[end:])
	return result.String(), nil
}

func (f *sourceFile) insertImports() {
	var standard, external []string
	for importPath, alias := range f.newImports {
		spec := strconv.Quote(importPath)
		if alias != path.Base(importPath) {
			spec = alias + " " + spec
		}
		if strings.Contains(strings.SplitN(importPath, "/", 2)[0], ".") {
			external = append(external, spec)
		} else {
			standard = append(standard, spec)
		}
	}
	slices.Sort(standard)
	slices.Sort(external)
	specs := strings.Join(standard, "\n")
	if len(standard) != 0 && len(external) != 0 {
		specs += "\n\n"
	}
	specs += strings.Join(external, "\n")
	for _, decl := range f.ast.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.IMPORT {
			break
		}
		// A cgo import must keep its preamble and remain a standalone import.
		if f.imports["C"] != nil && slices.Contains(group.Specs, ast.Spec(f.imports["C"])) {
			continue
		}
		if group.Lparen.IsValid() {
			if pos, ok := f.lineEnd(group.Lparen + 1); ok {
				f.edits = append(f.edits, edit{pos, pos, "\n" + specs})
				return
			}
			continue
		}
		if pos, ok := f.lineEnd(group.End()); ok {
			start := f.offset(group.Specs[0].Pos())
			f.edits = append([]edit{{start, start, "(\n" + specs + "\n\n"}, {pos, pos, "\n)"}}, f.edits...)
			return
		}
	}
	pos := f.offset(f.ast.Name.End())
	if end, ok := f.lineEnd(f.ast.Name.End()); ok {
		pos = end
		f.edits = append([]edit{{pos, pos, "\n\nimport (\n" + specs + "\n)"}}, f.edits...)
		return
	}
	// A file can put its package clause and imports on the same line.
	if tail := strings.TrimLeft(f.source[pos:], " \t"); strings.HasPrefix(tail, ";") {
		pos += len(f.source[pos:]) - len(tail) + 1
	}
	f.edits = append(f.edits, edit{pos, pos, "\n\nimport (\n" + specs + "\n)\n"})
}

// Removing or replacing a declaration may leave an empty block or unused import.
func cleanEditedSource(filename string, data []byte) ([]byte, error) {
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, filename, data, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	offset := func(pos token.Pos) int { return positions.PositionFor(pos, false).Offset }
	used := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Obj == nil {
				used[ident.Name] = true
			}
		}
		return true
	})
	var edits []edit
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		if group.Tok == token.VAR && len(group.Specs) == 0 &&
			len(bytes.TrimSpace(data[offset(group.Lparen)+1:offset(group.Rparen)])) == 0 {
			edits = append(edits, edit{start: offset(group.Pos()), end: offset(group.End())})
		}
		if group.Tok != token.IMPORT {
			continue
		}
		var unused []edit
		for _, spec := range group.Specs {
			spec := spec.(*ast.ImportSpec)
			name, _ := strconv.Unquote(spec.Path.Value)
			if name != "time" && name != runtimeImport {
				continue
			}
			alias := importName(name, spec)
			if alias == "." || alias == "_" || used[alias] {
				continue
			}
			end := offset(spec.End())
			if spec.Comment != nil {
				end = max(end, offset(spec.Comment.End()))
			}
			unused = append(unused, edit{start: offset(spec.Pos()), end: end})
		}
		if len(unused) != 0 && len(unused) == len(group.Specs) {
			end := max(offset(group.End()), unused[len(unused)-1].end)
			edits = append(edits, edit{start: offset(group.Pos()), end: end})
		} else {
			edits = append(edits, unused...)
		}
	}
	if len(edits) == 0 {
		return data, nil
	}
	result, err := applyEdits(string(data), edits)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}
	return format.Source([]byte(result))
}

func (f *sourceFile) write(data []byte) error {
	current, err := os.ReadFile(f.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if (err == nil) != (f.original != nil) || !bytes.Equal(current, f.original) {
		return fmt.Errorf("%s changed during generation", f.path)
	}
	temp, err := os.CreateTemp(filepath.Dir(f.path), ".modelgen-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err = temp.Chmod(f.mode); err != nil {
		return err
	}
	if _, err = temp.Write(data); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), f.path)
}
