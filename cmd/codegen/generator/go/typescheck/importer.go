package typescheck

import (
	"go/importer"
	"go/token"
	"go/types"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// daggerImportPath is the public Dagger Go SDK import path.
const daggerImportPath = "dagger.io/dagger"

// moduleDaggerImportSuffix is the trailing path every Dagger Go
// module's local generated types package ends with.
const moduleDaggerImportSuffix = "/internal/dagger"

// IsDaggerImport reports whether path refers to a Dagger types package
// — either the public SDK or a module's own internal/dagger directory.
func IsDaggerImport(path string) bool {
	return path == daggerImportPath || strings.HasSuffix(path, moduleDaggerImportSuffix)
}

// SchemaImporter implements types.Importer by returning a synthesized
// dagger package (built from an introspection schema) for any import
// path matching IsDaggerImport, and delegating everything else to a
// fallback (typically importer.Default for stdlib).
//
// Synthesized packages are cached per-path so repeated imports return
// the same *types.Package — go/types relies on pointer-equality of
// imported packages to recognise that *dagger.Container in two files
// refers to the same type.
type SchemaImporter struct {
	schema   *introspection.Schema
	fallback types.Importer

	// cache holds synthesized packages so repeated lookups return the
	// same *types.Package (go/types compares packages by pointer when
	// resolving cross-file references).
	cache map[string]*types.Package

	// stubs holds empty packages returned for unknown non-stdlib paths
	// in tolerant mode. Populating these lets type-checking proceed
	// when the user imports something we don't know about.
	stubs map[string]*types.Package

	// tolerant, when true, returns empty stub packages instead of an
	// error when an unknown import path is encountered. This lets
	// type-checking continue against partial information rather than
	// halting on the first unresolved import.
	tolerant bool
}

// NewSchemaImporter constructs a SchemaImporter that synthesizes
// dagger packages from schema and falls back to the default Go
// importer for everything else (stdlib, third-party).
//
// The importer pre-populates a minimal stub for `context` so its
// Context type is always resolvable even when the surrounding Go
// install lacks gc-mode binary archives — common in stripped-down
// codegen containers. Other stdlib paths still go through
// importer.Default; if that fails, tolerant mode returns an empty
// stub so type-checking can continue.
func NewSchemaImporter(schema *introspection.Schema) *SchemaImporter {
	imp := &SchemaImporter{
		schema:   schema,
		fallback: importer.Default(),
		cache:    map[string]*types.Package{},
		stubs:    map[string]*types.Package{},
		tolerant: true,
	}
	imp.cache["context"] = synthesizeContextPackage()
	return imp
}

// synthesizeContextPackage returns a minimal `context` package
// containing just the Context interface type. Module signatures
// reference context.Context as a sentinel arg the codegen drops, so
// astscan only needs the type to be identifiable by package + name —
// methods and bodies are unnecessary.
func synthesizeContextPackage() *types.Package {
	pkg := types.NewPackage("context", "context")
	scope := pkg.Scope()
	ctx := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Context", nil), nil, nil)
	ctx.SetUnderlying(types.NewInterfaceType(nil, nil))
	scope.Insert(ctx.Obj())
	pkg.MarkComplete()
	return pkg
}

// Import implements types.Importer.
func (i *SchemaImporter) Import(path string) (*types.Package, error) {
	if pkg, ok := i.cache[path]; ok {
		return pkg, nil
	}
	if IsDaggerImport(path) {
		pkg := SynthesizePackage(i.schema, path)
		i.cache[path] = pkg
		return pkg, nil
	}
	pkg, err := i.fallback.Import(path)
	if err == nil {
		return pkg, nil
	}
	if !i.tolerant {
		return nil, err
	}
	// Unknown / unloadable path: hand back an empty package so the
	// type-checker can continue. References through this package will
	// produce types we can't resolve, but that's no worse than the
	// AST-only resolver — and the rest of the file still type-checks.
	if stub, ok := i.stubs[path]; ok {
		return stub, nil
	}
	stub := types.NewPackage(path, lastSegment(path))
	stub.MarkComplete()
	i.stubs[path] = stub
	return stub, nil
}

// lastSegment returns the final '/'-separated segment of an import
// path, used as the synthesized package's display name when we have
// nothing better to call it.
func lastSegment(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
