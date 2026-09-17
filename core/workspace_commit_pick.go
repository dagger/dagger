package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

type WorkspaceCommitPickStatus string

var WorkspaceCommitPickStatuses = dagql.NewEnum[WorkspaceCommitPickStatus]()

var (
	WorkspaceCommitPickable  = WorkspaceCommitPickStatuses.Register("PICKABLE", "The commit can be applied.")
	WorkspaceCommitPicked    = WorkspaceCommitPickStatuses.Register("PICKED", "The commit is already present by hash or cherry-pick origin.")
	WorkspaceCommitRedundant = WorkspaceCommitPickStatuses.Register("REDUNDANT", "The patch is already present, or applying it would be empty.")
	WorkspaceCommitConflict  = WorkspaceCommitPickStatuses.Register("CONFLICT", "The commit cannot be applied; see reason and conflictPaths.")
)

func (WorkspaceCommitPickStatus) Type() *ast.Type {
	return &ast.Type{NamedType: "WorkspaceCommitPickStatus", NonNull: true}
}
func (WorkspaceCommitPickStatus) TypeDescription() string {
	return "Whether a source commit can be pulled."
}
func (WorkspaceCommitPickStatus) Decoder() dagql.InputDecoder { return WorkspaceCommitPickStatuses }
func (s WorkspaceCommitPickStatus) ToLiteral() call.Literal {
	return WorkspaceCommitPickStatuses.Literal(s)
}

type WorkspaceCommitPickReason string

var WorkspaceCommitPickReasons = dagql.NewEnum[WorkspaceCommitPickReason]()

var (
	WorkspaceCommitPickReasonNone    = WorkspaceCommitPickReasons.Register("NONE", "No conflict.")
	WorkspaceCommitPickReasonContent = WorkspaceCommitPickReasons.Register("CONTENT", "The patch conflicts with committed content.")
	WorkspaceCommitPickReasonDirty   = WorkspaceCommitPickReasons.Register("DIRTY", "The commit touches uncommitted paths in the receiving workspace.")
)

func (WorkspaceCommitPickReason) Type() *ast.Type {
	return &ast.Type{NamedType: "WorkspaceCommitPickReason", NonNull: true}
}
func (WorkspaceCommitPickReason) TypeDescription() string {
	return "Why a source commit cannot be pulled."
}
func (WorkspaceCommitPickReason) Decoder() dagql.InputDecoder { return WorkspaceCommitPickReasons }
func (r WorkspaceCommitPickReason) ToLiteral() call.Literal {
	return WorkspaceCommitPickReasons.Literal(r)
}

type WorkspaceCommitPick struct {
	Commit        dagql.ObjectResult[*GitCommit] `field:"true" doc:"The commit in the source workspace."`
	Status        WorkspaceCommitPickStatus      `field:"true" doc:"Whether this commit can be applied."`
	Reason        WorkspaceCommitPickReason      `field:"true" doc:"Why the commit conflicts, or NONE."`
	ConflictPaths []string                       `field:"true" doc:"Workspace-root-relative conflicting paths. Empty unless the status is CONFLICT."`
}

func (*WorkspaceCommitPick) Type() *ast.Type {
	return &ast.Type{NamedType: "WorkspaceCommitPick", NonNull: true}
}
func (*WorkspaceCommitPick) TypeDescription() string {
	return "A source commit classified against the receiving workspace."
}

func (p *WorkspaceCommitPick) AttachDependencyResults(ctx context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	attached, err := attach(p.Commit)
	if err != nil {
		return nil, err
	}
	commit, ok := attached.(dagql.ObjectResult[*GitCommit])
	if !ok {
		return nil, fmt.Errorf("unexpected commit dependency %T", attached)
	}
	p.Commit = commit
	return []dagql.AnyResult{commit}, nil
}
