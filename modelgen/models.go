package modelgen

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/metadata-driven/mdd/meta"
)

func (g *generator) plan(cfg meta.Config) error {
	models := cfg.Models()
	var enums []meta.Enum
	for _, model := range models {
		if err := g.bindName(model, goName(model.Name()), "RegisterModel", model.Items()); err != nil {
			return err
		}
		for _, attr := range model.Attrs() {
			if attr.Kind() == meta.KindEnum {
				enum := attr.(meta.Enum)
				if err := g.bindName(enum, goName(model.Name())+goName(enum.Name()), "RegisterEnum", enum.Items()); err != nil {
					return err
				}
				enums = append(enums, enum)
			}
		}
	}
	if err := g.packageSetup(); err != nil {
		return err
	}
	if err := g.enums(enums); err != nil {
		return err
	}
	for _, model := range models {
		if err := g.model(model); err != nil {
			return err
		}
	}
	return nil
}

func (g *generator) bindName(node meta.Node, fallback, register string, items []meta.Item) error {
	var name string
	for _, d := range g.pkg.calls[runtimeCall{name: register, path: node.Path()}] {
		name = d.target
	}
	for _, item := range items {
		for _, d := range g.pkg.calls[runtimeCall{name: "Item", path: item.Path()}] {
			if name != "" && name != d.target {
				return fmt.Errorf("%s is bound to both %s and %s", node.Path(), name, d.target)
			}
			name = d.target
		}
	}
	if name == "" {
		name = fallback
	}
	if !token.IsIdentifier(name) || name == "_" {
		return fmt.Errorf("%s cannot be represented as a Go type name", node.Path())
	}
	g.names[node.Path()] = name
	return nil
}

func (g *generator) packageSetup() error {
	if ds := g.pkg.symbols[symbol{name: "Meta"}]; len(ds) != 0 {
		if len(ds) != 1 || ds[0].kind != token.VAR || ds[0].file.constrained {
			return fmt.Errorf("Meta must be a package variable available in every build")
		}
	} else {
		file, err := g.pkg.file("meta.go")
		if err != nil {
			return err
		}
		name := "mddMetaYAML"
		for len(g.pkg.symbols[symbol{name: name}]) != 0 || file.hasImportName(name) {
			name += "_"
		}
		if file.imports["embed"] == nil {
			file.newImports["embed"] = "_"
		}
		parser := file.qualify(runtimeImport+"/meta", "MustParseYAML")
		pos := len(file.source)
		file.edits = append(file.edits, edit{pos, pos, fmt.Sprintf("\n\n//go:embed meta.yaml\nvar %s []byte %s\n\nvar Meta = %s(%s) %s\n", name, marker, parser, name, marker)})
	}
	g.claimed[symbol{name: "Meta"}] = "the metadata variable"
	file, err := g.pkg.file("context.go")
	if err != nil {
		return err
	}
	if declarations := g.pkg.symbols[symbol{name: "Context"}]; len(declarations) == 1 && !declarations[0].file.constrained {
		file = declarations[0].file
	}
	if err := g.group(file, token.TYPE, false, nil, typeMember("Context", "the model context", "Context")); err != nil {
		return err
	}
	return g.application(file)
}

func (g *generator) application(contextFile *sourceFile) error {
	file, err := g.pkg.file("app.go")
	if err != nil {
		return err
	}
	if err := g.group(file, token.TYPE, false, nil, typeMember("App", "the model application", "Application[Context]")); err != nil {
		return err
	}
	constructor := member{
		key:    symbol{name: "NewApp"},
		origin: "the application constructor",
		code: func(f *sourceFile) string {
			return fmt.Sprintf("func NewApp() (*App, error) { return %s[App](Meta) }", f.qualify(runtimeImport, "NewApp"))
		},
	}
	if err := g.group(file, token.FUNC, false, nil, constructor); err != nil {
		return err
	}
	accessor := methodMember("Context", "App", "the context application accessor", func(f *sourceFile) string {
		app := f.qualify(runtimeImport, "App")
		ctx := f.localName("ctx")
		return fmt.Sprintf("func (%s *Context) App() *App { return %s[App](%s) }", ctx, app, ctx)
	})
	return g.group(contextFile, token.FUNC, false, nil, accessor)
}

func (g *generator) enums(enums []meta.Enum) error {
	if len(enums) == 0 {
		return nil
	}
	file, err := g.pkg.file("enums.go")
	if err != nil {
		return err
	}
	var types []member
	for _, enum := range enums {
		types = append(types, typeMember(g.names[enum.Path()], enum.Path(), "EnumItem"))
	}
	if err := g.group(file, token.TYPE, false, func(d *declaration) bool { return embeds(d, "EnumItem") }, types...); err != nil {
		return err
	}
	for _, enum := range enums {
		name := g.names[enum.Path()]
		if len(enum.Items()) == 0 {
			if err := g.group(file, token.VAR, false, nil, bindingMember("_", name, "RegisterEnum", enum.Path())); err != nil {
				return err
			}
		}
		if err := g.items(file, enum, name, enum.Items()); err != nil {
			return err
		}
	}
	return nil
}

func (g *generator) items(file *sourceFile, owner meta.Node, name string, items []meta.Item) error {
	var values []member
	for _, item := range items {
		m := bindingMember(name+goName(item.Name()), name, "Item", item.Path())
		if ds := g.pkg.calls[m.call]; len(ds) == 1 && ds[0].key.name != "_" {
			// Existing code can choose a public name for an item.
			m.key = ds[0].key
		}
		values = append(values, m)
	}
	matches := func(d *declaration) bool {
		if d.call.name != "Item" || d.target != name {
			return false
		}
		dot := strings.LastIndexByte(d.call.path, '.')
		return dot >= 0 && d.call.path[:dot] == owner.Path()
	}
	return g.group(file, token.VAR, len(values) > 1, matches, values...)
}

func (g *generator) model(model meta.Model) error {
	name := g.names[model.Path()]
	filename := model.Name() + ".go"
	if constrainedFilename(filename) || strings.HasSuffix(filename, "_test.go") {
		filename = model.Name() + "_mdd.go"
	}
	file, err := g.pkg.file(filename)
	if err != nil {
		return err
	}
	key := symbol{name: name}
	declarations := g.pkg.symbols[key]
	if len(declarations) == 1 && !declarations[0].file.constrained {
		file = declarations[0].file
	}
	if err := g.group(file, token.TYPE, false, nil, typeMember(name, model.Path(), "Object")); err != nil {
		return err
	}
	modelType := func(d *declaration) bool { return d.kind == token.TYPE && d.key == key }
	if err := g.group(file, token.VAR, false, modelType, bindingMember("_", name, "RegisterModel", model.Path())); err != nil {
		return err
	}
	if set := model.SetName(); set != "" {
		if method := goName(set); method == "Context" || method == "MDDApp" {
			return fmt.Errorf("%s: set %q conflicts with the embedded mdd.Context API", model.Path(), set)
		}
		m := methodMember("Context", goName(set), model.Path()+" set", func(f *sourceFile) string {
			ctx := f.localName("ctx")
			return fmt.Sprintf("func (%s *Context) %s() *%s[%s] { return %s[%s](%s) }", ctx, goName(set), f.qualify(runtimeImport, "Set"), name, f.qualify(runtimeImport, "NewSet"), name, ctx)
		})
		if err := g.group(file, token.FUNC, false, nil, m); err != nil {
			return err
		}
	}
	if err := g.items(file, model, name, model.Items()); err != nil {
		return err
	}
	initial, _ := utf8.DecodeRuneInString(name)
	receiver := string(unicode.ToLower(initial))
	if err := g.accessors(file, model, name, receiver); err != nil {
		return err
	}
	if len(declarations) == 0 {
		g.addModelHooks(file, name, receiver)
	}
	return nil
}

func (g *generator) addModelHooks(file *sourceFile, name, receiver string) {
	var hooks strings.Builder
	receiver = file.localName(receiver)
	for _, hook := range []string{"OnNew", "OnDelete", "OnCommit", "OnAfterCommit"} {
		key := symbol{receiver: name, name: hook}
		if g.claimed[key] != "" || len(g.pkg.symbols[key]) != 0 || g.pkg.commented[key] {
			continue
		}
		fmt.Fprintf(&hooks, "// func (%s *%s) %s() {}\n", receiver, name, hook)
	}
	if hooks.Len() != 0 {
		pos := len(file.source)
		file.edits = append(file.edits, edit{pos, pos, "\n\n// Uncomment any model lifecycle hooks you want to implement.\n" + hooks.String()})
	}
}

func (g *generator) accessors(file *sourceFile, model meta.Model, name, receiver string) error {
	getters := []member{methodMember(name, "Context", model.Path()+" context", func(f *sourceFile) string {
		call := f.qualify(runtimeImport, "Ctx")
		obj := f.localName(receiver)
		return fmt.Sprintf("func (%s *%s) Context() *Context { return %s[Context](%s) }", obj, name, call, obj)
	})}
	if model.SetName() != "" {
		getters = append(getters, methodMember(name, "ID", model.Path()+".id", func(f *sourceFile) string {
			call := f.qualify(runtimeImport, "ID")
			obj := f.localName(receiver)
			return fmt.Sprintf("func (%s *%s) ID() uint64 { return %s(%s) }", obj, name, call, obj)
		}))
	}
	var setters, sets []member
	for _, attr := range model.Attrs() {
		if attr.Name() == "id" {
			continue
		}
		method := goName(attr.Name())
		switch method {
		case "Object", "MDDObject", "MDDContext", "Set":
			return fmt.Errorf("%s: accessor %s conflicts with the embedded mdd.Object API", attr.Path(), method)
		}
		info := attr.Type()
		switch {
		case info.Set:
			if info.Target.Kind() != meta.KindModel || info.Target.(meta.Model).SetName() == "" {
				return fmt.Errorf("%s: the Go runtime requires a set-backed model as the target of %s", attr.Path(), info.Expr)
			}
			sets = append(sets, methodMember(name, method, attr.Path(), func(f *sourceFile) string {
				set := f.qualify(runtimeImport, "Set")
				call := f.qualify(runtimeImport, "AttrSet")
				obj := f.localName(receiver)
				typ := g.names[info.Target.Path()]
				return fmt.Sprintf("func (%s *%s) %s() *%s[%s] { return %s[%s](%s, %q) }", obj, name, method, set, typ, call, typ, obj, attr.Name())
			}))
		default:
			getters = append(getters, methodMember(name, method, attr.Path(), func(f *sourceFile) string {
				call := f.qualify(runtimeImport, "Attr")
				typ := g.attrType(f, info)
				obj := f.localName(receiver)
				return fmt.Sprintf("func (%s *%s) %s() %s { return %s[%s](%s, %q) }", obj, name, method, typ, call, typ, obj, attr.Name())
			}))
			setMethod := "Set" + method
			setters = append(setters, methodMember(name, setMethod, attr.Path(), func(f *sourceFile) string {
				call := f.qualify(runtimeImport, "SetAttr")
				typ := "any"
				if !info.IsFile() {
					typ = g.attrType(f, info)
				}
				obj, value := f.localName(receiver), f.localName("v")
				if value == obj {
					value = f.localName(value + "_")
				}
				return fmt.Sprintf("func (%s *%s) %s(%s %s) *%s { return %s(%s, %q, %s) }", obj, name, setMethod, value, typ, name, call, obj, attr.Name(), value)
			}))
		}
	}
	if err := g.group(file, token.FUNC, false, accessorGroup(name, getters, false), getters...); err != nil {
		return err
	}
	if err := g.group(file, token.FUNC, false, accessorGroup(name, setters, true), setters...); err != nil {
		return err
	}
	return g.group(file, token.FUNC, false, accessorGroup(name, sets, false), sets...)
}

func accessorGroup(receiver string, members []member, setter bool) func(*declaration) bool {
	names := make(map[symbol]bool, len(members))
	for _, m := range members {
		names[m.key] = true
	}
	return func(d *declaration) bool {
		fn, ok := d.node.(*ast.FuncDecl)
		if !ok || d.key.receiver != receiver {
			return false
		}
		params, results := fn.Type.Params.NumFields(), fn.Type.Results.NumFields()
		if !setter {
			return names[d.key] && params == 0 && results == 1
		}
		if params != 1 || !strings.HasPrefix(d.key.name, "Set") {
			return false
		}
		if results == 0 {
			return true
		}
		if results != 1 {
			return false
		}
		result, ok := ast.Unparen(fn.Type.Results.List[0].Type).(*ast.StarExpr)
		return ok && baseType(result.X) == receiver
	}
}

func (g *generator) attrType(file *sourceFile, info meta.TypeInfo) string {
	var typ string
	if info.IsReference() {
		typ = "*" + g.names[info.Target.Path()]
	} else {
		switch info.PrimitiveTypeName() {
		case meta.TypeID:
			typ = "uint64"
		case meta.TypeBool:
			typ = "bool"
		case meta.TypeInt:
			typ = "int64"
		case meta.TypeNumber:
			typ = "float64"
		case meta.TypeDatetime:
			typ = file.qualify("time", "Time")
		case meta.TypeString:
			typ = "string"
		case meta.TypeBinary:
			typ = "[]byte"
		case meta.TypeFile:
			typ = file.qualify(runtimeImport, "File")
		case meta.TypeJSON:
			typ = "any"
		}
	}
	if info.Collection {
		return "[]" + typ
	}
	return typ
}

func goName(name string) string {
	var result strings.Builder
	for part := range strings.SplitSeq(name, "_") {
		if part == "" {
			continue
		}
		switch part {
		case "api", "css", "gbp", "eur", "html", "http", "https", "id", "ip", "json", "mdd", "sql", "ui", "uid", "url", "usd", "uuid", "xml":
			result.WriteString(strings.ToUpper(part))
		default:
			first, size := utf8.DecodeRuneInString(part)
			result.WriteRune(unicode.ToUpper(first))
			result.WriteString(part[size:])
		}
	}
	return result.String()
}
