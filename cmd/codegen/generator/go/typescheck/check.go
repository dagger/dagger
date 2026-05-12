// Package typescheck runs go/types over a Dagger module's source
// files using a schema-synthesized importer, so dagger imports
// resolve without the real Dagger Go SDK or `dagger.gen.go` being
// present on disk.
//
// The result is a *types.Info that astscan consumes for type
// resolution, replacing its earlier string-based selector matching
// with the same name- and method-set semantics the legacy
// packages.Load path provided.
package typescheck

import (
	"go/ast"
	"go/token"
	"go/types"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// Result bundles the outputs of a tolerant type-check.
type Result struct {
	// Pkg is the type-checked user package. Always non-nil; callers
	// can read package-scope objects from Pkg.Scope().
	Pkg *types.Package

	// Info carries per-expression type information. References that
	// failed to resolve are absent rather than producing errors — see
	// Errors for the full list of issues encountered.
	Info *types.Info

	// Errors is the set of type-checker errors encountered. Tolerant
	// mode lets type-checking continue past errors so info covers as
	// many expressions as possible; callers can still inspect Errors
	// to surface diagnostic information when wanted.
	Errors []error
}

// Check type-checks files under the given import path using a
// SchemaImporter for dagger imports. The check is tolerant: errors
// don't halt processing, and unresolvable imports become empty
// stubs. Errors encountered during the check are returned alongside
// the resulting type info.
//
// fset must be the FileSet that produced files; passing a different
// one yields meaningless source positions in error messages.
func Check(
	fset *token.FileSet,
	files []*ast.File,
	pkgPath string,
	schema *introspection.Schema,
) *Result {
	imp := NewSchemaImporter(schema)
	res := &Result{
		Info: &types.Info{
			Defs:       map[*ast.Ident]types.Object{},
			Uses:       map[*ast.Ident]types.Object{},
			Types:      map[ast.Expr]types.TypeAndValue{},
			Selections: map[*ast.SelectorExpr]*types.Selection{},
			Implicits:  map[ast.Node]types.Object{},
		},
	}
	cfg := &types.Config{
		Importer:                 imp,
		IgnoreFuncBodies:         true,
		DisableUnusedImportCheck: true,
		Error: func(err error) {
			res.Errors = append(res.Errors, err)
		},
	}
	pkg, _ := cfg.Check(pkgPath, fset, files, res.Info)
	if pkg == nil {
		// Even when Check returns a nil package on fatal failure
		// (rare in tolerant mode), construct an empty one so callers
		// can rely on Pkg being non-nil.
		pkg = types.NewPackage(pkgPath, "")
	}
	res.Pkg = pkg
	return res
}
