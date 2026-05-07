package typescheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// schema with a tiny Container that has a From method, enough to
// exercise selector resolution and method-set lookup.
func makeSchema() *introspection.Schema {
	return &introspection.Schema{
		Types: introspection.Types{
			{
				Kind: introspection.TypeKindObject,
				Name: "Container",
				Fields: []*introspection.Field{
					{
						Name: "from",
						Args: introspection.InputValues{
							{
								Name:    "address",
								TypeRef: &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "String"}},
							},
						},
						TypeRef: &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: &introspection.TypeRef{Kind: introspection.TypeKindObject, Name: "Container"}},
					},
				},
			},
			{Kind: introspection.TypeKindScalar, Name: "String"},
		},
	}
}

func TestCheck_resolvesDaggerSelector(t *testing.T) {
	src := `package mod

import "dagger.io/dagger"

type M struct{}

func (m *M) Build() *dagger.Container {
	return nil
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	res := Check(fset, []*ast.File{f}, "mod", makeSchema())

	// Find the Build method and assert its return type resolves to
	// *dagger.Container.
	var methodFunc *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "Build" {
			methodFunc = fd
			break
		}
	}
	if methodFunc == nil {
		t.Fatal("Build not found")
	}
	resultExpr := methodFunc.Type.Results.List[0].Type
	tav := res.Info.Types[resultExpr]
	if tav.Type == nil {
		t.Fatalf("result type not in Info.Types; errors: %v", res.Errors)
	}
	ptr, ok := tav.Type.(*types.Pointer)
	if !ok {
		t.Fatalf("expected *Container, got %T (%v)", tav.Type, tav.Type)
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok {
		t.Fatalf("expected named type under pointer, got %T", ptr.Elem())
	}
	if named.Obj().Name() != "Container" {
		t.Fatalf("expected Container, got %s", named.Obj().Name())
	}
	if path := named.Obj().Pkg().Path(); !IsDaggerImport(path) {
		t.Fatalf("expected dagger-import path, got %q", path)
	}
}

func TestCheck_tolerantUnknownImport(t *testing.T) {
	src := `package mod

import (
	"context"
	"dagger.io/dagger"
	weird "example.com/does-not-exist"
)

type M struct{}

func (m *M) Fn(ctx context.Context, x weird.Thing) *dagger.Container {
	return nil
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	res := Check(fset, []*ast.File{f}, "mod", makeSchema())
	// Tolerant mode: errors are expected (Thing is unresolved), but
	// the dagger.Container reference should still type-check.
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Fn" {
			continue
		}
		resultExpr := fd.Type.Results.List[0].Type
		tav := res.Info.Types[resultExpr]
		if tav.Type == nil {
			t.Fatalf("dagger.Container result didn't resolve in tolerant mode; errors: %v", res.Errors)
		}
	}
}
