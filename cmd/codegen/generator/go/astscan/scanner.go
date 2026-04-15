// Package astscan extracts a Dagger module's declared types from
// Go source code using go/parser + go/ast. It resolves type
// references against the module's imports and the supplied
// introspection schema; it does NOT invoke `go build` or
// packages.Load.
//
// See hack/designs/no-codegen-at-runtime-moduletypes.md.
package astscan

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/cmd/codegen/schematool"
)

// Scan parses all .go files in dir (non-recursive, skipping _test.go)
// and returns the declared module types.
//
// schema is used to resolve references like dagger.Container →
// an introspection.Type in the schema. moduleName is the module's
// name (as it will appear in the emitted ModuleTypes).
func Scan(dir, moduleName string, schema *introspection.Schema) (*schematool.ModuleTypes, error) {
	fset := token.NewFileSet()
	files, pkgName, err := parseFiles(fset, dir)
	if err != nil {
		return nil, err
	}
	return (&scanner{
		fset:       fset,
		files:      files,
		pkgName:    pkgName,
		schema:     schema,
		moduleName: moduleName,
	}).run()
}

type scanner struct {
	fset       *token.FileSet
	files      []*ast.File // all .go files of the module (non-test, sorted by path)
	pkgName    string      // name of the package the files belong to
	schema     *introspection.Schema
	moduleName string
}

// parseFiles reads .go files in dir (non-recursive, skipping _test.go)
// and groups them by package name, preferring "main" when present
// (Dagger modules use package main). Returned files are sorted by
// path so scans produce stable output.
func parseFiles(fset *token.FileSet, dir string) ([]*ast.File, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}
	// Group parsed files by their declared package name, keyed by path
	// so we can emit them in a deterministic order.
	type parsed struct {
		path string
		file *ast.File
	}
	pkgs := map[string][]parsed{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
			continue
		}
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, "", err
		}
		pkgs[file.Name.Name] = append(pkgs[file.Name.Name], parsed{path: path, file: file})
	}
	// Prefer `main` if present (Dagger modules use `package main`).
	var chosenName string
	var chosen []parsed
	if ps, ok := pkgs["main"]; ok {
		chosenName = "main"
		chosen = ps
	} else {
		for name, ps := range pkgs {
			chosenName = name
			chosen = ps
			break
		}
	}
	if chosen == nil {
		return nil, "empty", nil
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].path < chosen[j].path })
	out := make([]*ast.File, len(chosen))
	for i, p := range chosen {
		out[i] = p.file
	}
	return out, chosenName, nil
}

func (s *scanner) run() (*schematool.ModuleTypes, error) {
	out := &schematool.ModuleTypes{Name: s.moduleName}

	// Pass 1: collect all top-level struct, interface, and candidate
	// enum (typed-string) type names so later passes can resolve them
	// as same-module references.
	sameModule := map[string]string{} // type name → kind (OBJECT / INTERFACE / ENUM)
	for _, f := range s.files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				switch t := ts.Type.(type) {
				case *ast.StructType:
					sameModule[ts.Name.Name] = "OBJECT"
				case *ast.InterfaceType:
					sameModule[ts.Name.Name] = "INTERFACE"
				case *ast.Ident:
					// Typed string → candidate enum.
					if t.Name == "string" {
						sameModule[ts.Name.Name] = "ENUM"
					}
				}
			}
		}
	}

	// Pass 2: emit objects, interfaces, and enum declarations; collect
	// methods by receiver name.
	methods := map[string][]*ast.FuncDecl{} // receiver type name → methods
	methodFiles := map[*ast.FuncDecl]*ast.File{}

	// Track which enums have been declared, to know where to attach
	// values later.
	enumIndex := map[string]int{} // enum name → index in out.Enums

	for _, f := range s.files {
		imports := collectImports(f)
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if err := s.walkGenDecl(out, d, imports, sameModule, enumIndex); err != nil {
					return nil, err
				}
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) == 0 {
					continue
				}
				recvName := recvTypeName(d.Recv.List[0].Type)
				if recvName == "" {
					continue
				}
				if _, isSameMod := sameModule[recvName]; !isSameMod {
					continue
				}
				methods[recvName] = append(methods[recvName], d)
				methodFiles[d] = f
			}
		}
	}

	// Pass 3: walk const groups to collect enum values. Handles three
	// shapes:
	//   const StatusPending Status = "PENDING"              // vs.Type set
	//   const (
	//       StatusPending Status = "PENDING"                // vs.Type set
	//       StatusActive         = "ACTIVE"                 // inherits Status via lastType
	//   )
	//   const StatusCancelled = Status("CANCELLED")         // explicit-conversion RHS
	for _, f := range s.files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			// lastType tracks the most recent explicit type seen in the
			// current const block so bare specs can inherit it.
			var lastType *ast.Ident
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				// Determine which enum (if any) this spec contributes to.
				var enumName string
				if vs.Type != nil {
					if id, ok := vs.Type.(*ast.Ident); ok {
						lastType = id
						enumName = id.Name
					} else {
						// Not an Ident type; skip for enum purposes.
						continue
					}
				} else if lastType != nil {
					enumName = lastType.Name
				}
				// If vs.Type is nil, we may still be in Case B (explicit
				// conversion RHS) where the enum name is determined
				// per-value below. Only short-circuit on a confirmed non-
				// enum name.
				if enumName != "" {
					if _, ok := enumIndex[enumName]; !ok {
						continue
					}
				}
				// Pair each Name with its Value and emit.
				for i := range vs.Names {
					if i >= len(vs.Values) {
						break
					}
					value := vs.Values[i]
					// Case A / simple: inherited or explicit Ident type
					// with a BasicLit RHS.
					if lit, ok := value.(*ast.BasicLit); ok && enumName != "" {
						idx := enumIndex[enumName]
						out.Enums[idx].Values = append(out.Enums[idx].Values, schematool.EnumValue{
							Name:        trimQuotes(lit.Value),
							Description: strings.TrimSpace(vs.Doc.Text()),
						})
						continue
					}
					// Case B: explicit conversion RHS, e.g. Status("X").
					if call, ok := value.(*ast.CallExpr); ok && vs.Type == nil {
						callIdent, ok := call.Fun.(*ast.Ident)
						if !ok {
							continue
						}
						idx, ok := enumIndex[callIdent.Name]
						if !ok {
							continue
						}
						if len(call.Args) != 1 {
							continue
						}
						lit, ok := call.Args[0].(*ast.BasicLit)
						if !ok {
							continue
						}
						out.Enums[idx].Values = append(out.Enums[idx].Values, schematool.EnumValue{
							Name:        trimQuotes(lit.Value),
							Description: strings.TrimSpace(vs.Doc.Text()),
						})
					}
				}
			}
		}
	}

	// Pass 4: attach methods to objects and interfaces.
	for i, obj := range out.Objects {
		for _, m := range methods[obj.Name] {
			f := methodFiles[m]
			fn, err := s.walkMethod(m, collectImports(f), sameModule)
			if err != nil {
				return nil, err
			}
			if fn != nil {
				out.Objects[i].Functions = append(out.Objects[i].Functions, *fn)
			}
		}
	}

	return out, nil
}

// walkGenDecl handles TYPE declarations: struct → ObjectDef,
// interface → InterfaceDef (with its method set), typed string →
// EnumDef (values populated in a later pass).
func (s *scanner) walkGenDecl(
	out *schematool.ModuleTypes,
	gd *ast.GenDecl,
	imports map[string]string,
	sameModule map[string]string,
	enumIndex map[string]int,
) error {
	if gd.Tok != token.TYPE {
		return nil
	}
	for _, spec := range gd.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		// The spec's own doc takes precedence over the group doc when
		// both are present (e.g. type blocks with multiple specs each
		// having their own comment).
		doc := strings.TrimSpace(ts.Doc.Text())
		if doc == "" {
			doc = strings.TrimSpace(gd.Doc.Text())
		}

		switch st := ts.Type.(type) {
		case *ast.StructType:
			out.Objects = append(out.Objects, schematool.ObjectDef{
				Name:        ts.Name.Name,
				Description: doc,
			})
		case *ast.InterfaceType:
			iface := schematool.InterfaceDef{
				Name:        ts.Name.Name,
				Description: doc,
			}
			if st.Methods != nil {
				for _, field := range st.Methods.List {
					ft, ok := field.Type.(*ast.FuncType)
					if !ok {
						continue
					}
					// Method name: interface method fields list one name
					// per method.
					if len(field.Names) == 0 {
						continue
					}
					methodName := field.Names[0]
					if !methodName.IsExported() {
						continue
					}
					fn, err := s.walkFuncType(
						methodName.Name,
						strings.TrimSpace(field.Doc.Text()),
						ft,
						imports,
						sameModule,
					)
					if err != nil {
						return err
					}
					if fn != nil {
						iface.Functions = append(iface.Functions, *fn)
					}
				}
			}
			out.Interfaces = append(out.Interfaces, iface)
		case *ast.Ident:
			if st.Name == "string" {
				enumIndex[ts.Name.Name] = len(out.Enums)
				out.Enums = append(out.Enums, schematool.EnumDef{
					Name:        ts.Name.Name,
					Description: doc,
					Values:      []schematool.EnumValue{},
				})
			}
		}
	}
	return nil
}

// walkMethod walks a method decl and turns it into a schematool.Function.
// It skips unexported methods and drops context.Context args and
// `error` returns.
func (s *scanner) walkMethod(
	fd *ast.FuncDecl,
	imports map[string]string,
	sameModule map[string]string,
) (*schematool.Function, error) {
	if !fd.Name.IsExported() {
		return nil, nil
	}
	return s.walkFuncType(
		fd.Name.Name,
		strings.TrimSpace(fd.Doc.Text()),
		fd.Type,
		imports,
		sameModule,
	)
}

// walkFuncType is the shared core of method and interface-method
// extraction. name is the Go identifier; description is the doc
// comment text.
func (s *scanner) walkFuncType(
	name string,
	description string,
	ft *ast.FuncType,
	imports map[string]string,
	sameModule map[string]string,
) (*schematool.Function, error) {
	fn := &schematool.Function{
		Name:        lowerFirst(name),
		Description: description,
	}

	if ft.Params != nil {
		for _, field := range ft.Params.List {
			tref, err := s.resolveType(field.Type, imports, sameModule, field.Type.Pos())
			if err == errContextArg {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("function %s param: %w", name, err)
			}
			if len(field.Names) == 0 {
				// unnamed param; skip (Dagger modules don't use them).
				continue
			}
			for _, pname := range field.Names {
				fn.Args = append(fn.Args, schematool.FuncArg{
					Name:    pname.Name,
					TypeRef: tref,
				})
			}
		}
	}

	if ft.Results != nil {
		for _, res := range ft.Results.List {
			// `error` return → skip (Dagger treats error as out-of-band).
			if ident, ok := res.Type.(*ast.Ident); ok && ident.Name == "error" {
				continue
			}
			tref, err := s.resolveType(res.Type, imports, sameModule, res.Type.Pos())
			if err != nil {
				return nil, fmt.Errorf("function %s return: %w", name, err)
			}
			fn.ReturnType = tref
			break
		}
	}
	if fn.ReturnType == nil {
		// No non-error return (or no results at all): emit a Void return
		// type. Matches the existing Go codegen's TypeDefKindVoidKind
		// handling in cmd/codegen/generator/go/templates.
		fn.ReturnType = &schematool.TypeRef{
			Kind:   "NON_NULL",
			OfType: &schematool.TypeRef{Kind: "SCALAR", Name: "Void"},
		}
	}
	return fn, nil
}

// posf wraps fmt.Errorf with a "file:line:col: " prefix drawn from the
// scanner's FileSet. Callers use it whenever they have a token.Pos so
// module authors can jump directly to the offending source location.
func (s *scanner) posf(pos token.Pos, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if pos == token.NoPos || s.fset == nil {
		return fmt.Errorf("%s", msg)
	}
	p := s.fset.Position(pos)
	return fmt.Errorf("%s:%d:%d: %s", p.Filename, p.Line, p.Column, msg)
}

func collectImports(f *ast.File) map[string]string {
	imports := map[string]string{}
	for _, imp := range f.Imports {
		path := trimQuotes(imp.Path.Value)
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		} else {
			parts := strings.Split(path, "/")
			alias = parts[len(parts)-1]
		}
		imports[alias] = path
	}
	return imports
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func recvTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
