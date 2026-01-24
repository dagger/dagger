package core

import (
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
)

// Workspace represents the context in which a toolchain operates, providing
// access to the installer's filesystem, secrets, and other resources.
//
// When a toolchain constructor receives a Workspace argument, it receives
// access to the project that installed it. This is the foundation of the
// toolchain pattern, where toolchain modules receive and operate on their
// installer's source code and resources.
type Workspace struct {
	// The module source this workspace is scoped to.
	// This determines path resolution and content hashing.
	Source dagql.ObjectResult[*ModuleSource]
}

func (*Workspace) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Workspace",
		NonNull:   true,
	}
}

func (*Workspace) TypeDescription() string {
	return "The workspace in which a toolchain operates, providing access to the installer's filesystem, secrets, and other resources."
}

func (w *Workspace) Clone() *Workspace {
	cp := *w
	return &cp
}

// ModuleSource returns the underlying module source for this workspace.
func (w *Workspace) ModuleSource() *ModuleSource {
	return w.Source.Self()
}

// IsWorkspaceType returns true if the given TypeDef represents a Workspace type.
func IsWorkspaceType(td *TypeDef) bool {
	if td == nil {
		return false
	}
	if td.Kind != TypeDefKindObject {
		return false
	}
	if !td.AsObject.Valid {
		return false
	}
	return td.AsObject.Value.Name == "Workspace"
}

// ContainsWorkspace returns true if the given TypeDef contains a Workspace type,
// either directly or nested within fields. This is used for cache invalidation.
func ContainsWorkspace(td *TypeDef) bool {
	if td == nil {
		return false
	}

	switch td.Kind {
	case TypeDefKindObject:
		if !td.AsObject.Valid {
			return false
		}
		if td.AsObject.Value.Name == "Workspace" {
			return true
		}
		// Check all fields recursively
		for _, field := range td.AsObject.Value.Fields {
			if ContainsWorkspace(field.TypeDef) {
				return true
			}
		}
		return false

	case TypeDefKindList:
		if !td.AsList.Valid {
			return false
		}
		return ContainsWorkspace(td.AsList.Value.ElementTypeDef)

	default:
		return false
	}
}
