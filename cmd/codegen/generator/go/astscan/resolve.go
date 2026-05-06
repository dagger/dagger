package astscan

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/schematool"
)

// daggerImportPath is the public import path of the Dagger Go SDK,
// used by standalone client code.
const daggerImportPath = "dagger.io/dagger"

// moduleDaggerImportSuffix is the trailing path every Dagger Go
// module's local generated types package ends with. Codegen writes
// the module's dagger.gen.go under <modpath>/internal/dagger, so
// user code imports e.g. "dagger/my-module/internal/dagger".
const moduleDaggerImportSuffix = "/internal/dagger"

// isDaggerImport reports whether the given import path refers to a
// Dagger types package — either the public SDK or a module's own
// generated internal/dagger directory.
func isDaggerImport(path string) bool {
	return path == daggerImportPath || strings.HasSuffix(path, moduleDaggerImportSuffix)
}

// errContextArg is a sentinel error returned by resolveType when the
// expression is context.Context. Callers (e.g. walkFuncType) treat
// this as "skip this argument" rather than a failure.
var errContextArg = fmt.Errorf("context.Context")

// builtinTypes maps Go primitive identifiers to GraphQL SCALAR kinds.
var builtinTypes = map[string]string{
	"string":  "SCALAR",
	"int":     "SCALAR",
	"int32":   "SCALAR",
	"int64":   "SCALAR",
	"float32": "SCALAR",
	"float64": "SCALAR",
	"bool":    "SCALAR",
}

// resolveType converts a Go AST type expression into a
// schematool.TypeRef. imports maps local aliases (package names) to
// their import paths. sameModuleTypes is the set of names declared
// in the module itself — these resolve as their declared kind
// (OBJECT / INTERFACE / ENUM). pos is the source position of the
// expression, used to produce file:line:col-prefixed error messages
// when resolution fails; callers typically pass expr.Pos().
func (s *scanner) resolveType(expr ast.Expr, imports map[string]string, sameModuleTypes map[string]string, pos token.Pos) (*schematool.TypeRef, error) {
	switch t := expr.(type) {
	case *ast.StarExpr:
		// Pointer: Dagger's Go codegen treats *T as optional. Unwrap the
		// outermost NON_NULL of the inner resolution so the emitted type
		// is nullable.
		inner, err := s.resolveType(t.X, imports, sameModuleTypes, t.X.Pos())
		if err != nil {
			return nil, err
		}
		if inner != nil && inner.Kind == "NON_NULL" {
			return inner.OfType, nil
		}
		return inner, nil
	case *ast.ArrayType:
		inner, err := s.resolveType(t.Elt, imports, sameModuleTypes, t.Elt.Pos())
		if err != nil {
			return nil, err
		}
		return &schematool.TypeRef{
			Kind: "NON_NULL",
			OfType: &schematool.TypeRef{
				Kind:   "LIST",
				OfType: nonNull(inner),
			},
		}, nil
	case *ast.Ident:
		return s.resolveIdent(t, sameModuleTypes, pos)
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			return nil, s.posf(pos, "unsupported selector: %T", t.X)
		}
		return s.resolveSelector(pkg.Name, t.Sel.Name, imports, pos)
	}
	return nil, s.posf(pos, "unsupported type expression: %T", expr)
}

func (s *scanner) resolveIdent(id *ast.Ident, sameModuleTypes map[string]string, pos token.Pos) (*schematool.TypeRef, error) {
	if kind, ok := builtinTypes[id.Name]; ok {
		return nonNull(&schematool.TypeRef{Kind: kind, Name: scalarName(id.Name)}), nil
	}
	if kind, ok := sameModuleTypes[id.Name]; ok {
		return nonNull(&schematool.TypeRef{Kind: kind, Name: id.Name}), nil
	}
	if pos == token.NoPos {
		pos = id.Pos()
	}
	return nil, s.posf(pos, "unresolved identifier %q", id.Name)
}

func (s *scanner) resolveSelector(pkgAlias, typeName string, imports map[string]string, pos token.Pos) (*schematool.TypeRef, error) {
	path, ok := imports[pkgAlias]
	if !ok {
		return nil, s.posf(pos, "unknown import alias %q", pkgAlias)
	}
	if path == "context" && typeName == "Context" {
		// Special-cased: contexts are a sentinel arg in Dagger; the
		// caller drops them from the emitted function signature.
		return nil, errContextArg
	}
	if isDaggerImport(path) {
		if s.schema == nil {
			return nil, s.posf(pos, "type %s.%s cannot be resolved: no introspection schema provided", pkgAlias, typeName)
		}
		t := s.schema.Types.Get(typeName)
		if t == nil {
			return nil, s.posf(pos, "type %s.%s not found in introspection schema", pkgAlias, typeName)
		}
		return nonNull(&schematool.TypeRef{Kind: string(t.Kind), Name: typeName}), nil
	}
	return nil, s.posf(pos, "unsupported external type %s.%s (import %q)", pkgAlias, typeName, path)
}

func scalarName(goName string) string {
	switch goName {
	case "string":
		return "String"
	case "int", "int32", "int64":
		return "Int"
	case "float32", "float64":
		return "Float"
	case "bool":
		return "Boolean"
	}
	return goName
}

func nonNull(t *schematool.TypeRef) *schematool.TypeRef {
	if t == nil {
		return nil
	}
	if strings.EqualFold(t.Kind, "NON_NULL") {
		return t
	}
	return &schematool.TypeRef{Kind: "NON_NULL", OfType: t}
}
