package astscan

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/generator/go/typescheck"
	"github.com/dagger/dagger/cmd/codegen/schematool"
)

// errContextArg is a sentinel error returned by resolveType when the
// expression is context.Context. Callers (e.g. walkFuncType) treat
// this as "skip this argument" rather than a failure.
var errContextArg = fmt.Errorf("context.Context")

// resolveType converts a Go AST type expression into a
// schematool.TypeRef using the type information produced by
// typescheck. The expression must come from a file that was passed
// to typescheck.Check so info.Types[expr] is populated.
//
// sameModule maps same-module type names to their GraphQL kind so
// resolveType can stamp the right Kind on local references; this
// information isn't recoverable from types.Type alone (a typed-string
// "enum" looks identical to any other typed-string).
//
// pos is used only for error positions; callers typically pass
// expr.Pos().
func (s *scanner) resolveType(expr ast.Expr, sameModule map[string]string, pos token.Pos) (*schematool.TypeRef, error) {
	t := s.typeOf(expr)
	if t == nil || isInvalid(t) {
		// Type-checker couldn't resolve this expression. Surface the
		// original source form (e.g. "foreign.Thing") so module
		// authors can act on the diagnostic.
		if sel, ok := unwrapToSelector(expr); ok {
			return nil, s.posf(pos, "unsupported external type %s (import not resolved)", sel)
		}
		return nil, s.posf(pos, "unresolved type %s", exprString(expr))
	}
	return s.convertType(t, sameModule, pos)
}

// isInvalid reports whether t is types.Typ[types.Invalid] anywhere
// reachable through pointer/slice wrappers. go/types uses Invalid as
// a placeholder when an expression refers to something it couldn't
// resolve (e.g. a name in a stub-imported package).
func isInvalid(t types.Type) bool {
	switch x := t.(type) {
	case *types.Pointer:
		return isInvalid(x.Elem())
	case *types.Slice:
		return isInvalid(x.Elem())
	case *types.Array:
		return isInvalid(x.Elem())
	case *types.Basic:
		return x.Kind() == types.Invalid
	}
	return false
}

// unwrapToSelector returns the "pkg.Name" form of a type expression
// once any star/array wrappers are stripped. Used to produce
// position-aware diagnostics for foreign types.
func unwrapToSelector(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return unwrapToSelector(t.X)
	case *ast.ArrayType:
		return unwrapToSelector(t.Elt)
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok {
			return x.Name + "." + t.Sel.Name, true
		}
	}
	return "", false
}

// typeOf looks up the resolved Go type of an expression. Returns nil
// when the type-checker didn't process the expression (bad syntax,
// unresolvable selector, etc.) — callers turn this into a helpful
// error pointing at the source location.
func (s *scanner) typeOf(expr ast.Expr) types.Type {
	if s.info == nil {
		return nil
	}
	if tav, ok := s.info.Types[expr]; ok && tav.Type != nil {
		return tav.Type
	}
	return nil
}

// convertType is the recursive core of resolveType. It walks a
// types.Type and emits the matching schematool.TypeRef, applying
// Dagger's optional-via-pointer convention.
//
// Aliases (`type X = Y`) are looked through up front so the rest of
// the dispatch handles only canonical types. Without this, an alias
// to a Dagger type would slip past the *types.Named branch and hit
// the unsupported-type fallback.
func (s *scanner) convertType(t types.Type, sameModule map[string]string, pos token.Pos) (*schematool.TypeRef, error) {
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Pointer:
		// Pointers are optional in Dagger's Go SDK convention. Drop the
		// outermost NON_NULL on the inner resolution.
		inner, err := s.convertType(t.Elem(), sameModule, pos)
		if err != nil {
			return nil, err
		}
		if inner != nil && inner.Kind == "NON_NULL" {
			return inner.OfType, nil
		}
		return inner, nil

	case *types.Slice:
		inner, err := s.convertType(t.Elem(), sameModule, pos)
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

	case *types.Array:
		inner, err := s.convertType(t.Elem(), sameModule, pos)
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

	case *types.Basic:
		// Builtins (string, int, …) → SCALAR with the corresponding
		// GraphQL name.
		name := scalarName(t.Name())
		if name == "" {
			return nil, s.posf(pos, "unsupported basic type %s", t.Name())
		}
		return nonNull(&schematool.TypeRef{Kind: "SCALAR", Name: name}), nil

	case *types.Named:
		return s.convertNamed(t, sameModule, pos)
	}
	return nil, s.posf(pos, "unsupported type %s", t.String())
}

// convertNamed routes a *types.Named to the right schema lookup
// based on which package it comes from. Three cases:
//
//	universe.error             → caller's responsibility (we never see it
//	                             in args, and walkFuncType filters it out
//	                             of returns)
//	context.Context            → errContextArg sentinel (caller skips)
//	dagger.io/dagger.<Name>    → look up Name in the introspection schema
//	<userPkg>.<Name>           → look up in sameModule
//	other                      → unsupported (matches the previous resolver)
func (s *scanner) convertNamed(n *types.Named, sameModule map[string]string, pos token.Pos) (*schematool.TypeRef, error) {
	obj := n.Obj()
	name := obj.Name()
	pkg := obj.Pkg()

	if pkg == nil {
		// Universe-scope names: error is the only one we care about.
		if name == "error" {
			return nil, fmt.Errorf("error type")
		}
		return nil, s.posf(pos, "unresolved universe type %q", name)
	}

	path := pkg.Path()

	if path == "context" && name == "Context" {
		return nil, errContextArg
	}

	if typescheck.IsDaggerImport(path) {
		if s.schema == nil {
			return nil, s.posf(pos, "type %s.%s cannot be resolved: no introspection schema provided", pkg.Name(), name)
		}
		t := s.schema.Types.Get(name)
		if t == nil {
			return nil, s.posf(pos, "type %s.%s not found in introspection schema", pkg.Name(), name)
		}
		return nonNull(&schematool.TypeRef{Kind: string(t.Kind), Name: name}), nil
	}

	if pkg == s.userPkg {
		kind, ok := sameModule[name]
		if !ok {
			return nil, s.posf(pos, "unresolved local type %q", name)
		}
		return nonNull(&schematool.TypeRef{Kind: kind, Name: name}), nil
	}

	return nil, s.posf(pos, "unsupported external type %s.%s (import %q)", pkg.Name(), name, path)
}

// scalarName maps a Go builtin name to its GraphQL SCALAR name.
func scalarName(goName string) string {
	switch goName {
	case "string":
		return "String"
	case "int", "int32", "int64", "int16", "int8":
		return "Int"
	case "float32", "float64":
		return "Float"
	case "bool":
		return "Boolean"
	}
	return ""
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

// exprString renders an AST expression to its source-equivalent
// string for diagnostic messages. Falls back to a generic descriptor
// if the renderer can't handle the node.
func exprString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok {
			return x.Name + "." + t.Sel.Name
		}
		return "<selector>." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.ArrayType:
		return "[]" + exprString(t.Elt)
	}
	return fmt.Sprintf("%T", expr)
}
