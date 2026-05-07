package typescheck

import (
	"go/token"
	"go/types"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// SynthesizePackage builds a *types.Package matching the import path
// the user wrote (e.g. "dagger.io/dagger" or
// "dagger/<mod>/internal/dagger") from an introspection schema, so
// go/types can resolve user references like *dagger.Container without
// the real Dagger Go SDK being present on disk.
//
// Method bodies are not synthesized — only signatures, which is all
// go/types needs to type-check expressions in user code.
func SynthesizePackage(schema *introspection.Schema, path string) *types.Package {
	pkg := types.NewPackage(path, "dagger")
	scope := pkg.Scope()

	// Pass 1: declare all named types with placeholder underlyings so
	// later passes can reference them. Methods and field signatures
	// often reference other named types, so we need every name in
	// scope before building signatures.
	named := map[string]*types.Named{}
	basics := map[string]types.Type{}
	for _, t := range schema.Types {
		switch t.Kind {
		case introspection.TypeKindObject, introspection.TypeKindInterface:
			n := types.NewNamed(types.NewTypeName(token.NoPos, pkg, t.Name, nil), nil, nil)
			n.SetUnderlying(types.NewStruct(nil, nil))
			scope.Insert(n.Obj())
			named[t.Name] = n
		case introspection.TypeKindEnum:
			// Enums are typed strings.
			n := types.NewNamed(types.NewTypeName(token.NoPos, pkg, t.Name, nil), nil, nil)
			n.SetUnderlying(types.Typ[types.String])
			scope.Insert(n.Obj())
			named[t.Name] = n
		case introspection.TypeKindScalar:
			basics[t.Name] = scalarToBasic(t.Name)
		case introspection.TypeKindInputObject:
			n := types.NewNamed(types.NewTypeName(token.NoPos, pkg, t.Name, nil), nil, nil)
			n.SetUnderlying(types.NewStruct(nil, nil))
			scope.Insert(n.Obj())
			named[t.Name] = n
		}
	}

	// Pass 2: build method signatures on objects/interfaces. Each schema
	// Field becomes a method on the receiver named type. Field args
	// become parameters; the field's TypeRef becomes the result type.
	for _, t := range schema.Types {
		if t.Kind != introspection.TypeKindObject && t.Kind != introspection.TypeKindInterface {
			continue
		}
		recv := named[t.Name]
		if recv == nil {
			continue
		}
		for _, f := range t.Fields {
			if !exported(f.Name) {
				continue
			}
			sig := buildSignature(pkg, named, basics, recv, f)
			method := types.NewFunc(token.NoPos, pkg, goFieldName(f.Name), sig)
			recv.AddMethod(method)
		}
	}

	// Required for go/types to treat the package as importable;
	// without it the type-checker reports "undefined: <pkg>" for any
	// SelectorExpr that resolves through this package.
	pkg.MarkComplete()
	return pkg
}

// scalarToBasic maps GraphQL scalar names to their Go basic-type
// representations. Unknown scalars become an empty interface so user
// code that handles them doesn't fail to type-check.
func scalarToBasic(name string) types.Type {
	switch name {
	case "String", "ID":
		return types.Typ[types.String]
	case "Int":
		return types.Typ[types.Int]
	case "Float":
		return types.Typ[types.Float64]
	case "Boolean":
		return types.Typ[types.Bool]
	case "Void":
		return types.NewStruct(nil, nil)
	}
	return types.NewInterfaceType(nil, nil)
}

// buildSignature converts an introspection Field into a *types.Signature
// usable as a method on recv. Method bodies are absent — only the
// signature shape is needed for type-checking user code.
func buildSignature(
	pkg *types.Package,
	named map[string]*types.Named,
	basics map[string]types.Type,
	recv *types.Named,
	f *introspection.Field,
) *types.Signature {
	params := make([]*types.Var, 0, len(f.Args))
	for _, a := range f.Args {
		t := refToType(pkg, named, basics, a.TypeRef)
		params = append(params, types.NewParam(token.NoPos, pkg, goArgName(a.Name), t))
	}
	results := []*types.Var{
		types.NewParam(token.NoPos, pkg, "", refToType(pkg, named, basics, f.TypeRef)),
	}
	recvVar := types.NewParam(token.NoPos, pkg, "self", types.NewPointer(recv))
	return types.NewSignatureType(
		recvVar,
		nil, nil,
		types.NewTuple(params...),
		types.NewTuple(results...),
		false,
	)
}

// refToType walks an introspection.TypeRef and produces the matching
// Go type. NON_NULL becomes a value type; the absence of NON_NULL
// becomes a pointer (Dagger's "optional"). LIST becomes a slice.
func refToType(
	pkg *types.Package,
	named map[string]*types.Named,
	basics map[string]types.Type,
	ref *introspection.TypeRef,
) types.Type {
	if ref == nil {
		return types.NewInterfaceType(nil, nil)
	}
	if ref.Kind == introspection.TypeKindNonNull {
		return refToTypeInner(pkg, named, basics, ref.OfType)
	}
	inner := refToTypeInner(pkg, named, basics, ref)
	if _, isBasic := inner.(*types.Basic); isBasic {
		// Basics aren't pointer-wrapped in the Dagger SDK — they're
		// dereferenced at the API boundary. Keep them as values.
		return inner
	}
	return types.NewPointer(inner)
}

func refToTypeInner(
	pkg *types.Package,
	named map[string]*types.Named,
	basics map[string]types.Type,
	ref *introspection.TypeRef,
) types.Type {
	if ref == nil {
		return types.NewInterfaceType(nil, nil)
	}
	switch ref.Kind {
	case introspection.TypeKindList:
		return types.NewSlice(refToType(pkg, named, basics, ref.OfType))
	case introspection.TypeKindScalar, introspection.TypeKindEnum:
		if t, ok := basics[ref.Name]; ok {
			return t
		}
		if n, ok := named[ref.Name]; ok {
			return n
		}
		return scalarToBasic(ref.Name)
	default:
		if n, ok := named[ref.Name]; ok {
			return n
		}
		return types.NewInterfaceType(nil, nil)
	}
}

// goFieldName converts a GraphQL field name (lowerCamelCase) into the
// exported Go method name (UpperCamelCase). The Dagger SDK's codegen
// uses this same mapping when emitting bindings.
func goFieldName(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 32
	}
	return string(r)
}

// goArgName mirrors goFieldName for parameter names. Reserved Go
// keywords get a trailing underscore so the generated package
// type-checks; we don't actually emit user-facing code from the
// synthesized package, so the exact name doesn't matter for
// correctness.
func goArgName(s string) string {
	if isGoKeyword(s) {
		return s + "_"
	}
	return s
}

func isGoKeyword(s string) bool {
	switch s {
	case "break", "case", "chan", "const", "continue", "default",
		"defer", "else", "fallthrough", "for", "func", "go", "goto",
		"if", "import", "interface", "map", "package", "range",
		"return", "select", "struct", "switch", "type", "var":
		return true
	}
	return false
}

func exported(name string) bool {
	if name == "" {
		return false
	}
	c := name[0]
	return c >= 'A' && c <= 'Z'
}
