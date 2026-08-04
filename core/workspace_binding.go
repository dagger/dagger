package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

// InheritedWorkspaceBinding is the trusted, execution-scoped workspace context
// passed to a nested Dagger client.
//
// Workspace is retained as a real dependency so lazy execs and services can
// derive a fresh session handle after persistence. The remaining fields
// identify the live ancestor binding whose workspace module state may be
// inherited.
type InheritedWorkspaceBinding struct {
	Workspace dagql.ObjectResult[*Workspace]

	BindingID string

	WorkspaceEnv    string
	HasWorkspaceEnv bool
}

func inheritedWorkspaceBindingID(binding *InheritedWorkspaceBinding) string {
	if binding == nil {
		return ""
	}
	return binding.BindingID
}

func inheritedWorkspaceEnv(binding *InheritedWorkspaceBinding) string {
	if binding == nil {
		return ""
	}
	return binding.WorkspaceEnv
}

func CaptureInheritedWorkspaceBinding(
	ctx context.Context,
	input dagql.Optional[dagql.ID[*Workspace]],
) (*InheritedWorkspaceBinding, error) {
	if !input.Valid {
		return nil, nil
	}

	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("get Dagger server for inherited workspace: %w", err)
	}
	workspace, err := input.Value.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("load inherited workspace: %w", err)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query for inherited workspace: %w", err)
	}
	binding, err := query.Server.CaptureInheritedWorkspaceBinding(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("capture inherited workspace binding: %w", err)
	}
	return binding, nil
}

func nestedClientMetadataForExec(
	clientMetadata *engine.ClientMetadata,
	execMD *engineutil.ExecutionMetadata,
	experimentalPrivilegedNesting bool,
	inheritedWorkspace *InheritedWorkspaceBinding,
) *engine.ClientMetadata {
	hasInheritedWorkspace := inheritedWorkspace != nil && inheritedWorkspace.Workspace.Self() != nil
	if !experimentalPrivilegedNesting && !hasInheritedWorkspace {
		return nil
	}

	nested := &engine.ClientMetadata{
		ClientID:          identity.NewID(),
		ClientVersion:     engine.Version,
		SessionID:         clientMetadata.SessionID,
		AllowedLLMModules: slices.Clone(clientMetadata.AllowedLLMModules),
		LockMode:          clientMetadata.LockMode,
	}
	if execMD != nil {
		nested.UseRecipeIDsByDefault = execMD.UseRecipeIDsByDefault
	}
	if inheritedWorkspace != nil {
		nested.InheritedWorkspaceBindingID = inheritedWorkspace.BindingID
		nested.InheritedWorkspaceEnv = inheritedWorkspace.WorkspaceEnv
		nested.InheritedWorkspaceEnvSet = inheritedWorkspace.HasWorkspaceEnv
	}
	return nested
}
