package core

import (
	"context"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
)

// Context provides access to the execution context of a module, including
// filesystem, secrets, and other contextual resources.
//
// When a function receives a Context argument, it receives the caller's context,
// allowing it to operate on the caller's behalf. This is the foundation of
// the toolchain pattern, where toolchain modules receive and operate on their
// caller's source code and resources.
type Context struct {
	// The module source this context is scoped to.
	// This determines path resolution and content hashing.
	Source dagql.ObjectResult[*ModuleSource]
}

func (*Context) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Context",
		NonNull:   true,
	}
}

func (*Context) TypeDescription() string {
	return "The execution context of a module, providing access to filesystem, secrets, and other contextual resources."
}

func (c *Context) Clone() *Context {
	cp := *c
	return &cp
}

// ModuleSource returns the underlying module source for this context.
func (c *Context) ModuleSource() *ModuleSource {
	return c.Source.Self()
}

// contextKey is the Go context key for ContextMetadata.
type contextKey struct{}

// ContextMetadata holds context information that is passed through Go context.
// This is used to track whose context should be injected when calling functions
// that have Context arguments.
type ContextMetadata struct {
	// SourceID is the call ID of the ModuleSource to use for this context.
	// When set, Context arguments will be constructed using this source
	// instead of the current module's source.
	SourceID *dagql.ID[*ModuleSource]
}

// WithContextMetadata attaches ContextMetadata to a Go context.
func WithContextMetadata(ctx context.Context, cm *ContextMetadata) context.Context {
	return context.WithValue(ctx, contextKey{}, cm)
}

// ContextMetadataFromContext retrieves ContextMetadata from a Go context.
func ContextMetadataFromContext(ctx context.Context) (*ContextMetadata, bool) {
	cm, ok := ctx.Value(contextKey{}).(*ContextMetadata)
	return cm, ok
}

// IsContextType returns true if the given TypeDef represents a Context type.
func IsContextType(td *TypeDef) bool {
	if td == nil {
		return false
	}
	if td.Kind != TypeDefKindObject {
		return false
	}
	if !td.AsObject.Valid {
		return false
	}
	return td.AsObject.Value.Name == "Context"
}

// ContainsContext returns true if the given TypeDef contains a Context type,
// either directly or nested within fields. This is used for cache invalidation.
func ContainsContext(td *TypeDef) bool {
	if td == nil {
		return false
	}

	switch td.Kind {
	case TypeDefKindObject:
		if !td.AsObject.Valid {
			return false
		}
		if td.AsObject.Value.Name == "Context" {
			return true
		}
		// Check all fields recursively
		for _, field := range td.AsObject.Value.Fields {
			if ContainsContext(field.TypeDef) {
				return true
			}
		}
		return false

	case TypeDefKindList:
		if !td.AsList.Valid {
			return false
		}
		return ContainsContext(td.AsList.Value.ElementTypeDef)

	default:
		return false
	}
}
