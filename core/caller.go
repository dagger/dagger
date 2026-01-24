package core

import (
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
)

// Caller represents the calling module's context, providing access to its
// filesystem, secrets, and other resources.
//
// When a function receives a Caller argument, it receives the caller's context,
// allowing it to operate on the caller's behalf. This is the foundation of
// the toolchain pattern, where toolchain modules receive and operate on their
// caller's source code and resources.
type Caller struct {
	// The module source this caller is scoped to.
	// This determines path resolution and content hashing.
	Source dagql.ObjectResult[*ModuleSource]
}

func (*Caller) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Caller",
		NonNull:   true,
	}
}

func (*Caller) TypeDescription() string {
	return "The calling module's context, providing access to its filesystem, secrets, and other resources."
}

func (c *Caller) Clone() *Caller {
	cp := *c
	return &cp
}

// ModuleSource returns the underlying module source for this caller.
func (c *Caller) ModuleSource() *ModuleSource {
	return c.Source.Self()
}

// IsCallerType returns true if the given TypeDef represents a Caller type.
func IsCallerType(td *TypeDef) bool {
	if td == nil {
		return false
	}
	if td.Kind != TypeDefKindObject {
		return false
	}
	if !td.AsObject.Valid {
		return false
	}
	return td.AsObject.Value.Name == "Caller"
}

// ContainsCaller returns true if the given TypeDef contains a Caller type,
// either directly or nested within fields. This is used for cache invalidation.
func ContainsCaller(td *TypeDef) bool {
	if td == nil {
		return false
	}

	switch td.Kind {
	case TypeDefKindObject:
		if !td.AsObject.Valid {
			return false
		}
		if td.AsObject.Value.Name == "Caller" {
			return true
		}
		// Check all fields recursively
		for _, field := range td.AsObject.Value.Fields {
			if ContainsCaller(field.TypeDef) {
				return true
			}
		}
		return false

	case TypeDefKindList:
		if !td.AsList.Valid {
			return false
		}
		return ContainsCaller(td.AsList.Value.ElementTypeDef)

	default:
		return false
	}
}
