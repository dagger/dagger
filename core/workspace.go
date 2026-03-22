package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

type workspaceKey struct{}

// WorkspaceIDToContext stores a workspace ID in the context, allowing
// module function calls to resolve workspace arguments against the
// caller's workspace rather than the module container's filesystem.
func WorkspaceIDToContext(ctx context.Context, wsID *call.ID) context.Context {
	return context.WithValue(ctx, workspaceKey{}, wsID)
}

// WorkspaceFromContext retrieves the workspace from context, if set.
func WorkspaceFromContext(ctx context.Context, srv *dagql.Server) (dagql.ObjectResult[*Workspace], bool) {
	wsID, ok := ctx.Value(workspaceKey{}).(*call.ID)
	if !ok || wsID == nil {
		return dagql.ObjectResult[*Workspace]{}, false
	}
	res, err := dagql.NewID[*Workspace](wsID).Load(ctx, srv)
	if err != nil {
		return dagql.ObjectResult[*Workspace]{}, false
	}
	return res, true
}

// Workspace represents a detected workspace in the dagql schema.
type Workspace struct {
	Root string `field:"true" doc:"Absolute path to the workspace root directory."`

	// ClientID is the ID of the client that created this workspace.
	// Used to route host filesystem operations through the correct session
	// when the workspace is passed to a module function.
	ClientID string `field:"true" doc:"The client ID that owns this workspace's host filesystem."`

	// Branch is the Git branch this workspace is on.
	Branch string `field:"true" doc:"The Git branch this workspace is on."`

	// RepoRoot is the path to the main repo (where .git/ lives).
	// Not exposed in the schema. Needed to create worktrees.
	RepoRoot string
}

func (*Workspace) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Workspace",
		NonNull:   true,
	}
}

func (*Workspace) TypeDescription() string {
	return "A Dagger workspace detected from the current working directory."
}

func (ws *Workspace) Clone() *Workspace {
	cp := *ws
	return &cp
}
