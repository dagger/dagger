package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// WorkspaceMigration describes the explicit migration plan for a workspace.
type WorkspaceMigration struct {
	Changes          dagql.ObjectResult[*Changeset] `field:"true" doc:"Filesystem changes for the full migration plan."`
	Steps            []*WorkspaceMigrationStep      `field:"true" doc:"Logical migration steps, each identified by a stable code."`
	ModuleCandidates []string                       `field:"true" doc:"Unselected legacy module directories relative to the workspace root. Candidates can include fixtures."`
	ConfigFile       string                         `field:"true" doc:"Native workspace config path after migration, relative to the workspace root. Empty if no workspace config exists."`
}

// WorkspaceMigrationStep describes one logical migration step.
type WorkspaceMigrationStep struct {
	Code        string                         `field:"true" doc:"Stable code identifying this logical migration step."`
	Description string                         `field:"true" doc:"Generic summary of this step's purpose and impact."`
	Warnings    []string                       `field:"true" doc:"Non-fatal warnings raised while planning this step."`
	Changes     dagql.ObjectResult[*Changeset] `field:"true" doc:"Filesystem changes for this step."`
}

// Retain the snapshots used by the plan and its steps for as long as the plan
// is retained. Steps are metadata; their changesets are cache-backed objects.
func (m *WorkspaceMigration) AttachDependencyResults(
	ctx context.Context,
	self dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	changes, err := attachMigrationChanges(&m.Changes, attach)
	if err != nil {
		return nil, err
	}
	deps := []dagql.AnyResult{changes}
	for _, step := range m.Steps {
		stepDeps, err := step.AttachDependencyResults(ctx, self, attach)
		if err != nil {
			return nil, err
		}
		deps = append(deps, stepDeps...)
	}
	return deps, nil
}

func (s *WorkspaceMigrationStep) AttachDependencyResults(
	_ context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	changes, err := attachMigrationChanges(&s.Changes, attach)
	if err != nil {
		return nil, err
	}
	return []dagql.AnyResult{changes}, nil
}

func attachMigrationChanges(changes *dagql.ObjectResult[*Changeset], attach func(dagql.AnyResult) (dagql.AnyResult, error)) (dagql.AnyResult, error) {
	attached, err := attach(*changes)
	if err != nil {
		return nil, fmt.Errorf("attach migration changes: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Changeset])
	if !ok {
		return nil, fmt.Errorf("attach migration changes: unexpected result %T", attached)
	}
	*changes = typed
	return typed, nil
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
