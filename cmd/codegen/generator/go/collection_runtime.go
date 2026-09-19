package gogenerator

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/generator/go/templates"
	"golang.org/x/tools/go/packages"
)

const collectionRuntimeBase = "daggerCollectionBase"

// PrepareCollectionRuntime adds private collection state only to the runtime
// build. Author source and generated schema remain unchanged. A real struct
// field preserves state through Go value copies, including value receivers.
func PrepareCollectionRuntime(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return err
	}
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	collections := map[*ast.StructType]string{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		files[path] = file
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typ := spec.(*ast.TypeSpec)
				doc := typ.Doc
				if doc == nil {
					doc = gen.Doc
				}
				enabled, err := templates.CollectionPragma(doc.Text())
				if err != nil {
					return fmt.Errorf("collection %s: %w", typ.Name.Name, err)
				}
				if !enabled {
					continue
				}
				strct, ok := typ.Type.(*ast.StructType)
				if !ok {
					return fmt.Errorf("collection %s must be a struct", typ.Name.Name)
				}
				collections[strct] = typ.Name.Name
			}
		}
	}
	if len(collections) == 0 {
		return nil
	}
	pkgs, err := packages.Load(&packages.Config{
		Dir: dir, Fset: fset, Mode: packages.LoadSyntax,
		ParseFile: func(fset *token.FileSet, path string, src []byte) (*ast.File, error) {
			if file := files[path]; file != nil {
				return file, nil
			}
			return parser.ParseFile(fset, path, src, parser.ParseComments)
		},
	}, ".")
	if err != nil {
		return err
	}
	if len(pkgs) != 1 {
		return fmt.Errorf("expected one runtime package, got %d", len(pkgs))
	}
	pkg := pkgs[0]
	if len(pkg.Errors) > 0 {
		return fmt.Errorf("load collection runtime: %s", pkg.Errors[0])
	}
	collectionTypes := map[types.Type]bool{}
	for typ, name := range collections {
		if pkg.TypesInfo.TypeOf(typ) == nil {
			continue // Excluded by build constraints.
		}
		collectionTypes[pkg.TypesInfo.TypeOf(typ)] = true
		for _, field := range typ.Fields.List {
			for _, fieldName := range field.Names {
				if fieldName.Name == collectionRuntimeBase {
					return fmt.Errorf("collection %s uses reserved field %s", name, collectionRuntimeBase)
				}
			}
		}
		typ.Fields.List = append(typ.Fields.List, &ast.Field{
			Names: []*ast.Ident{ast.NewIdent(collectionRuntimeBase)}, Type: ast.NewIdent("string"),
		})
	}
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok || len(lit.Elts) == 0 {
				return true
			}
			typ := pkg.TypesInfo.TypeOf(lit)
			if ptr, ok := typ.(*types.Pointer); ok {
				typ = ptr.Elem()
			}
			if collectionTypes[typ.Underlying()] {
				if _, keyed := lit.Elts[0].(*ast.KeyValueExpr); !keyed {
					lit.Elts = append(lit.Elts, &ast.BasicLit{Kind: token.STRING, Value: `""`})
				}
			}
			return true
		})
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil {
				continue
			}
			recv := fn.Recv.List[0].Type
			if ptr, ok := recv.(*ast.StarExpr); ok {
				recv = ptr.X
			}
			name, ok := recv.(*ast.Ident)
			if !ok || !collectionTypes[pkg.TypesInfo.TypeOf(recv).Underlying()] || (fn.Name.Name != "MarshalJSON" && fn.Name.Name != "UnmarshalJSON") {
				continue
			}
			// Both generated methods use a concrete transport struct. Extend it
			// here instead of exposing runtime state in generated author bindings.
			found := false
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				spec, ok := node.(*ast.ValueSpec)
				if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "concrete" {
					return true
				}
				strct, ok := spec.Type.(*ast.StructType)
				if !ok {
					return true
				}
				strct.Fields.List = append(strct.Fields.List, &ast.Field{
					Names: []*ast.Ident{ast.NewIdent("DaggerCollectionBase")}, Type: ast.NewIdent("string"),
					Tag: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(`json:"__daggerCollectionBase,omitempty"`)},
				})
				found = true
				return false
			})
			if !found {
				return fmt.Errorf("collection %s has an unsupported %s method", name.Name, fn.Name.Name)
			}
			state := &ast.SelectorExpr{X: ast.NewIdent(fn.Recv.List[0].Names[0].Name), Sel: ast.NewIdent(collectionRuntimeBase)}
			transport := &ast.SelectorExpr{X: ast.NewIdent("concrete"), Sel: ast.NewIdent("DaggerCollectionBase")}
			var lhs, rhs ast.Expr = transport, state
			if fn.Name.Name == "UnmarshalJSON" {
				lhs, rhs = state, transport
			}
			assign := &ast.AssignStmt{Lhs: []ast.Expr{lhs}, Tok: token.ASSIGN, Rhs: []ast.Expr{rhs}}
			last := len(fn.Body.List) - 1
			fn.Body.List = append(fn.Body.List[:last], assign, fn.Body.List[last])
		}
		var out bytes.Buffer
		if err := format.Node(&out, fset, file); err != nil {
			return err
		}
		if err := os.WriteFile(fset.Position(file.Pos()).Filename, out.Bytes(), 0600); err != nil {
			return err
		}
	}
	return nil
}
