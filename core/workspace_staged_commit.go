package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// WorkspaceStagedCommit is the read-only view of one commit staged engine-side
// on a workspace (see WorkspacePendingCommit), as exposed by
// WorkspaceGit.stagedCommits.
type WorkspaceStagedCommit struct {
	SHA         string `field:"true" name:"sha" doc:"The full hash of the staged commit."`
	Origin      string `field:"true" name:"origin" doc:"The hash of the commit this one was replayed from, when it was pulled from another workspace; empty when it was authored here."`
	Message     string `field:"true" name:"message" doc:"The full commit message, subject and body."`
	Date        string `field:"true" name:"date" doc:"The RFC3339 author and committer date the commit was made with."`
	AuthorName  string `field:"true" name:"authorName" doc:"The author and committer name the commit was made with."`
	AuthorEmail string `field:"true" name:"authorEmail" doc:"The author and committer email the commit was made with."`

	// Changes is exactly what this commit folded in: a changeset from the
	// state staged by the previous commit to the state staged by this one.
	Changes dagql.ObjectResult[*Changeset] `field:"true" name:"changes" doc:"The changes this commit folded in, relative to the state staged before it."`
}

var (
	_ dagql.HasDependencyResults   = (*WorkspaceStagedCommit)(nil)
	_ dagql.PersistedObject        = (*WorkspaceStagedCommit)(nil)
	_ dagql.PersistedObjectDecoder = (*WorkspaceStagedCommit)(nil)
)

func (*WorkspaceStagedCommit) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceStagedCommit",
		NonNull:   true,
	}
}

func (*WorkspaceStagedCommit) TypeDescription() string {
	return "A commit staged in a workspace but not yet saved to the local checkout."
}

func (c *WorkspaceStagedCommit) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	if c == nil || c.Changes.Self() == nil {
		return nil, nil
	}
	attached, err := attach(c.Changes)
	if err != nil {
		return nil, fmt.Errorf("attach workspace staged commit changes: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Changeset])
	if !ok {
		return nil, fmt.Errorf("attach workspace staged commit changes: unexpected result %T", attached)
	}
	c.Changes = typed
	return []dagql.AnyResult{typed}, nil
}

// persistedWorkspaceStagedCommitPayload is the on-disk encoding of a
// WorkspaceStagedCommit: its metadata plus a reference to the changeset it
// folded in, which is itself persistable.
type persistedWorkspaceStagedCommitPayload struct {
	SHA            string `json:"sha,omitempty"`
	Origin         string `json:"origin,omitempty"`
	Message        string `json:"message,omitempty"`
	Date           string `json:"date,omitempty"`
	AuthorName     string `json:"authorName,omitempty"`
	AuthorEmail    string `json:"authorEmail,omitempty"`
	ChangesResulID uint64 `json:"changesResultID,omitempty"`
}

func (c *WorkspaceStagedCommit) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if c == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace staged commit: nil workspace staged commit")
	}
	payload := persistedWorkspaceStagedCommitPayload{
		SHA:         c.SHA,
		Origin:      c.Origin,
		Message:     c.Message,
		Date:        c.Date,
		AuthorName:  c.AuthorName,
		AuthorEmail: c.AuthorEmail,
	}
	if c.Changes.Self() != nil {
		changesID, err := encodePersistedObjectRef(cache, c.Changes, "workspace staged commit changes")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.ChangesResulID = changesID
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted workspace staged commit payload: %w", err)
	}
	return encodePersistedObjectRawJSON(payloadBytes), nil
}

func (*WorkspaceStagedCommit) DecodePersistedObject(
	ctx context.Context,
	dag *dagql.Server,
	_ uint64,
	_ *dagql.ResultCall,
	payload json.RawMessage,
) (dagql.Typed, error) {
	var persisted persistedWorkspaceStagedCommitPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted workspace staged commit payload: %w", err)
	}
	c := &WorkspaceStagedCommit{
		SHA:         persisted.SHA,
		Origin:      persisted.Origin,
		Message:     persisted.Message,
		Date:        persisted.Date,
		AuthorName:  persisted.AuthorName,
		AuthorEmail: persisted.AuthorEmail,
	}
	if persisted.ChangesResulID != 0 {
		changes, err := loadPersistedObjectResultByResultID[*Changeset](ctx, dag, persisted.ChangesResulID, "workspace staged commit changes")
		if err != nil {
			return nil, err
		}
		c.Changes = changes
	}
	return c, nil
}
