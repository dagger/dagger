package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

type Binding struct {
	Key         string
	Value       dagql.Typed
	Description string
	// The expected type
	// Used when defining an output
	ExpectedType string
}

func (*Binding) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Binding",
		NonNull:   true,
	}
}

func (b *Binding) AsObject() (dagql.AnyObjectResult, bool) {
	obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](b.Value)
	return obj, ok
}

// Return the stable object ID for this binding, or an empty string if it's not an object
func (b *Binding) ID() string {
	return b.Key
}

// A Dagql hook for installing module object types into the environment's
// schema.
type EnvHook struct {
	Server *dagql.Server
}

// We don't expose these types to modules SDK codegen, but
// we still want their graphql schemas to be available for
// internal usage. So we use this list to scrub them from
// the introspection JSON that module SDKs use for codegen.
var TypesHiddenFromModuleSDKs = []dagql.Typed{
	&Engine{},
	&EngineCache{},
	&EngineCacheEntry{},
	&EngineCacheEntrySet{},
}

func (s EnvHook) ModuleWithObject(ctx context.Context, mod *Module, targetTypedef dagql.ObjectResult[*TypeDef]) (*Module, error) {
	// Install the target type into the module.
	return mod.WithObject(ctx, targetTypedef)
}
