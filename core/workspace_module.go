package core

import (
	"sort"

	"github.com/vektah/gqlparser/v2/ast"
)

// WorkspaceModule describes a module entry in the workspace config.
type WorkspaceModule struct {
	Name       string `field:"true" doc:"The module name."`
	Entrypoint bool   `field:"true" doc:"Whether the module is the workspace entrypoint (functions aliased to Query root)."`
	Source     string `field:"true" doc:"The module source path."`
}

func (*WorkspaceModule) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceModule",
		NonNull:   true,
	}
}

func (*WorkspaceModule) TypeDescription() string {
	return "A module entry in the workspace configuration."
}

type WorkspaceModules []*WorkspaceModule

func (m WorkspaceModules) Sort() {
	sort.Slice(m, func(i, j int) bool {
		return m[i].Name < m[j].Name
	})
}
