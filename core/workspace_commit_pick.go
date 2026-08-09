package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

// WorkspaceCommitPickStatus is the verdict Workspace.commitsFrom reached about
// one of the source workspace's staged commits.
type WorkspaceCommitPickStatus string

var WorkspaceCommitPickStatuses = dagql.NewEnum[WorkspaceCommitPickStatus]()

var (
	WorkspaceCommitPickable = WorkspaceCommitPickStatuses.Register("PICKABLE",
		`The commit applies cleanly to this workspace and would be staged.`)
	WorkspaceCommitPicked = WorkspaceCommitPickStatuses.Register("PICKED",
		`This workspace already has the commit: in its own staged stack, in its git history, or as a commit it already replayed from the same origin.`)
	WorkspaceCommitRedundant = WorkspaceCommitPickStatuses.Register("REDUNDANT",
		`Applying the commit would change nothing: its content is already present, for instance because the same edit was made here by hand.`)
	WorkspaceCommitConflict = WorkspaceCommitPickStatuses.Register("CONFLICT",
		`The commit cannot be applied; see reason and conflictPaths.`)
)

func (WorkspaceCommitPickStatus) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceCommitPickStatus",
		NonNull:   true,
	}
}

func (WorkspaceCommitPickStatus) TypeDescription() string {
	return "Whether one of another workspace's staged commits can be applied to this one."
}

func (WorkspaceCommitPickStatus) Decoder() dagql.InputDecoder {
	return WorkspaceCommitPickStatuses
}

func (s WorkspaceCommitPickStatus) ToLiteral() call.Literal {
	return WorkspaceCommitPickStatuses.Literal(s)
}

// WorkspaceCommitPickReason says why a CONFLICT commit cannot be applied. It
// is a plain enum with a NONE member rather than a nullable field, so callers
// can switch over it exhaustively without a null case.
type WorkspaceCommitPickReason string

var WorkspaceCommitPickReasons = dagql.NewEnum[WorkspaceCommitPickReason]()

var (
	WorkspaceCommitPickReasonNone = WorkspaceCommitPickReasons.Register("NONE",
		`No obstruction: the status is not CONFLICT.`)
	WorkspaceCommitPickReasonContent = WorkspaceCommitPickReasons.Register("CONTENT",
		`The commit's patch no longer applies to this workspace's content.`)
	WorkspaceCommitPickReasonDirty = WorkspaceCommitPickReasons.Register("DIRTY",
		`This workspace has uncommitted changes on a path the commit touches, so applying it would sweep them into someone else's commit.`)
)

func (WorkspaceCommitPickReason) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceCommitPickReason",
		NonNull:   true,
	}
}

func (WorkspaceCommitPickReason) TypeDescription() string {
	return "Why a staged commit from another workspace cannot be applied to this one."
}

func (WorkspaceCommitPickReason) Decoder() dagql.InputDecoder {
	return WorkspaceCommitPickReasons
}

func (r WorkspaceCommitPickReason) ToLiteral() call.Literal {
	return WorkspaceCommitPickReasons.Literal(r)
}

// WorkspaceCommitPick is what Workspace.commitsFrom decided about one staged
// commit of the source workspace. The metadata mirrors WorkspaceStagedCommit,
// describing the commit as it exists in the *source*: replaying it here
// rewrites its parent and therefore its hash, but keeps everything else.
type WorkspaceCommitPick struct {
	SHA         string `field:"true" name:"sha" doc:"The full hash of the commit in the source workspace."`
	Origin      string `field:"true" name:"origin" doc:"The hash of the commit the source commit was itself replayed from; empty when it was authored in the source workspace."`
	Message     string `field:"true" name:"message" doc:"The full commit message, subject and body."`
	Date        string `field:"true" name:"date" doc:"The RFC3339 author and committer date the commit was made with."`
	AuthorName  string `field:"true" name:"authorName" doc:"The author and committer name the commit was made with."`
	AuthorEmail string `field:"true" name:"authorEmail" doc:"The author and committer email the commit was made with."`

	Status WorkspaceCommitPickStatus `field:"true" name:"status" doc:"Whether this commit can be applied to the receiving workspace."`
	Reason WorkspaceCommitPickReason `field:"true" name:"reason" doc:"Why the commit conflicts, or NONE when it does not."`
	// ConflictPaths is only meaningful for CONFLICT. It is never empty for one:
	// a CONTENT conflict falls back to the commit's own touched paths when git's
	// diagnostics cannot be parsed, so callers always have something to name.
	ConflictPaths []string `field:"true" name:"conflictPaths" doc:"The paths that obstruct this commit: the dirty paths for DIRTY, the paths the patch failed on for CONTENT. Empty unless the status is CONFLICT."`

	// Changes is what the commit folded in *in the source workspace* — the
	// patch source the plan was computed from, so rendering a per-commit
	// diffstat costs nothing extra.
	Changes dagql.ObjectResult[*Changeset] `field:"true" name:"changes" doc:"The changes this commit folded in, as recorded in the source workspace."`
}

// Unlike WorkspaceStagedCommit, a pick is not a PersistedObject: it is a
// transient projection of a plan, referenced by nothing that gets persisted.
// It does carry a live Changeset though, whose snapshots have to stay attached
// for the caller to read it.
var _ dagql.HasDependencyResults = (*WorkspaceCommitPick)(nil)

func (*WorkspaceCommitPick) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceCommitPick",
		NonNull:   true,
	}
}

func (*WorkspaceCommitPick) TypeDescription() string {
	return "One of another workspace's staged commits, classified against this workspace."
}

func (p *WorkspaceCommitPick) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	if p == nil || p.Changes.Self() == nil {
		return nil, nil
	}
	attached, err := attach(p.Changes)
	if err != nil {
		return nil, fmt.Errorf("attach workspace commit pick changes: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Changeset])
	if !ok {
		return nil, fmt.Errorf("attach workspace commit pick changes: unexpected result %T", attached)
	}
	p.Changes = typed
	return []dagql.AnyResult{typed}, nil
}
