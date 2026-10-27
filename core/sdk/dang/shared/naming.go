package dangshared

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

type namingViewKey struct{}

// WithModuleNaming records, for NamingSelectors, the naming view of the
// module whose typedefs the Dang runtime is about to register (see
// core.ModuleSource.NamingView).
//
// A module's runtime normally registers its typedefs over the API, at the
// view of the engine version the module declares, and the typedef
// constructors pick the naming rules from that view (see
// core.NamerFromContext). The Dang runtime runs in the engine and selects
// those constructors directly, which records no view unless the selector
// carries one.
func WithModuleNaming(ctx context.Context, src *core.ModuleSource) context.Context {
	return context.WithValue(ctx, namingViewKey{}, src.NamingView())
}

// ModuleNamer is the naming rules of the module recorded by
// WithModuleNaming, or the latest rules.
func ModuleNamer(ctx context.Context) core.Namer {
	if view, ok := ctx.Value(namingViewKey{}).(call.View); ok {
		return core.NamerForView(view)
	}
	return core.LatestNamer
}

// NamingSelectors stamps the view recorded by WithModuleNaming on the
// selectors of typedef constructors that normalize names (see
// core.IsNamingField), so they name the module's types, fields and arguments
// by its naming rules. sels is a chain selected from srv's root; other
// selectors are left alone.
func NamingSelectors(ctx context.Context, srv *dagql.Server, sels ...dagql.Selector) []dagql.Selector {
	view, ok := ctx.Value(namingViewKey{}).(call.View)
	if !ok {
		return sels
	}
	typeName := srv.Root().Type().Name()
	out := make([]dagql.Selector, len(sels))
	for i, sel := range sels {
		if core.IsNamingField(typeName, sel.Field) && sel.View == "" {
			sel.View = view
		}
		out[i] = sel
		typeName = selectedTypeName(srv, typeName, sel)
	}
	return out
}

// selectedTypeName is the name of the type sel selects from typeName, or ""
// if it isn't an object field srv knows.
func selectedTypeName(srv *dagql.Server, typeName string, sel dagql.Selector) string {
	objType, ok := srv.ObjectType(typeName)
	if !ok {
		return ""
	}
	spec, ok := objType.FieldSpec(sel.Field, sel.View)
	if !ok {
		return ""
	}
	return spec.Type.Type().Name()
}
