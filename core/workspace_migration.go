package core

import "github.com/vektah/gqlparser/v2/ast"

// WorkspaceMigration describes one explicit workspace migration flow.
type WorkspaceMigration struct {
	Code        string     `field:"true" doc:"Stable migration code identifying the migration flow."`
	Description string     `field:"true" doc:"Generic summary of the migration's purpose and impact."`
	Warnings    []string   `field:"true" doc:"Non-fatal warnings raised while planning this migration."`
	Changes     *Changeset `field:"true" doc:"Filesystem changes needed for this migration."`
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
