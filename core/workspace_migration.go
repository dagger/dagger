package core

import "github.com/vektah/gqlparser/v2/ast"

// WorkspaceMigration describes the explicit migration plan for a workspace.
type WorkspaceMigration struct {
	Changes *Changeset                 `field:"true" doc:"Filesystem changes for the full migration plan."`
	Steps   []*WorkspaceMigrationStep `field:"true" doc:"Logical migration steps, each identified by a stable code."`
}

// WorkspaceMigrationStep describes one logical migration step.
type WorkspaceMigrationStep struct {
	Code        string     `field:"true" doc:"Stable code identifying this logical migration step."`
	Description string     `field:"true" doc:"Generic summary of this step's purpose and impact."`
	Warnings    []string   `field:"true" doc:"Non-fatal warnings raised while planning this step."`
	Changes     *Changeset `field:"true" doc:"Filesystem changes for this step."`
}

func (*WorkspaceMigration) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceMigration",
		NonNull:   true,
	}
}

func (*WorkspaceMigration) TypeDescription() string {
	return "A planned workspace migration."
}

func (*WorkspaceMigrationStep) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceMigrationStep",
		NonNull:   true,
	}
}

func (*WorkspaceMigrationStep) TypeDescription() string {
	return "A single logical part of a workspace migration."
}
