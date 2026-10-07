package dangshared

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

type namingViewKey struct{}

// WithModuleNaming records, for NamingSelectors, the engine version of the
// module whose typedefs the Dang runtime is about to register.
//
// A module's runtime normally registers its typedefs over the API, at the
// view of the engine version the module declares, and the typedef
// constructors pick the naming rules from that view (see
// core.NamerFromContext). The Dang runtime runs in the engine and selects
// those constructors directly, which records no view unless the selector
// carries one.
func WithModuleNaming(ctx context.Context, engineVersion string) context.Context {
	return context.WithValue(ctx, namingViewKey{}, call.View(engine.APIViewVersion(engineVersion)))
}

// ModuleNamer is the naming rules of the module recorded by
// WithModuleNaming, or the latest rules.
func ModuleNamer(ctx context.Context) core.Namer {
	if view, ok := ctx.Value(namingViewKey{}).(call.View); ok {
		return core.NamerForView(view)
	}
	return core.LatestNamer
}

// namingFields are the typedef constructors that normalize the name they're
// given, and so need the module's view.
var namingFields = map[string]bool{
	"function":       true,
	"withArg":        true,
	"withObject":     true,
	"withInterface":  true,
	"withEnum":       true,
	"withScalar":     true,
	"withField":      true,
	"withEnumMember": true,
}

// NamingSelectors stamps the view recorded by WithModuleNaming on the
// selectors of typedef constructors that normalize names, so they name the
// module's types, fields and arguments by the rules of its engine version.
// Other selectors are left alone.
func NamingSelectors(ctx context.Context, sels ...dagql.Selector) []dagql.Selector {
	view, ok := ctx.Value(namingViewKey{}).(call.View)
	if !ok {
		return sels
	}
	out := make([]dagql.Selector, len(sels))
	for i, sel := range sels {
		if namingFields[sel.Field] && sel.View == "" {
			sel.View = view
		}
		out[i] = sel
	}
	return out
}
