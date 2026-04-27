package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
)

type RemoteGitMirror struct {
	RemoteURL string

	mu       sync.Mutex
	snapshot bkcache.MutableRef
}

var _ dagql.PersistedObject = (*RemoteGitMirror)(nil)
var _ dagql.PersistedObjectDecoder = (*RemoteGitMirror)(nil)
var _ dagql.OnReleaser = (*RemoteGitMirror)(nil)

func NewRemoteGitMirror(remoteURL string) *RemoteGitMirror {
	return &RemoteGitMirror{RemoteURL: remoteURL}
}

func (*RemoteGitMirror) Type() *ast.Type {
	return &ast.Type{
		NamedType: "RemoteGitMirror",
		NonNull:   true,
	}
}

func (*RemoteGitMirror) TypeDescription() string {
	return "An internal persistent bare git mirror."
}

func (mirror *RemoteGitMirror) OnRelease(ctx context.Context) error {
	if mirror == nil {
		return nil
	}
	mirror.mu.Lock()
	defer mirror.mu.Unlock()
	if mirror.snapshot == nil {
		return nil
	}
	err := mirror.snapshot.Release(ctx)
	mirror.snapshot = nil
	return err
}

func (mirror *RemoteGitMirror) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
	if mirror == nil {
		return nil
	}
	mirror.mu.Lock()
	defer mirror.mu.Unlock()
	if mirror.snapshot == nil {
		return nil
	}
	return []dagql.PersistedSnapshotRefLink{{
		RefKey: mirror.snapshot.SnapshotID(),
		Role:   "bare_repo",
	}}
}

func (mirror *RemoteGitMirror) CacheUsageMayChange() bool {
	return true
}

func (mirror *RemoteGitMirror) CacheUsageIdentities() []string {
	if mirror == nil {
		return nil
	}
	mirror.mu.Lock()
	defer mirror.mu.Unlock()
	if mirror.snapshot == nil {
		return nil
	}
	return []string{mirror.snapshot.SnapshotID()}
}

func (mirror *RemoteGitMirror) CacheUsageSize(ctx context.Context, identity string) (int64, bool, error) {
	if mirror == nil {
		return 0, false, nil
	}
	mirror.mu.Lock()
	snapshot := mirror.snapshot
	mirror.mu.Unlock()
	if snapshot == nil || snapshot.SnapshotID() != identity {
		return 0, false, nil
	}
	size, err := snapshot.Size(ctx)
	if err != nil {
		return 0, false, err
	}
	return size, true, nil
}

type persistedRemoteGitMirrorPayload struct {
	RemoteURL string `json:"remoteURL"`
}

func (mirror *RemoteGitMirror) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	_ = cache
	if mirror == nil {
		return nil, fmt.Errorf("encode persisted remote git mirror: nil mirror")
	}
	return json.Marshal(persistedRemoteGitMirrorPayload{
		RemoteURL: mirror.RemoteURL,
	})
}

func (*RemoteGitMirror) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedRemoteGitMirrorPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted remote git mirror payload: %w", err)
	}
	mirror := NewRemoteGitMirror(persisted.RemoteURL)
	if resultID == 0 {
		return mirror, nil
	}
	link, err := loadPersistedSnapshotLinkByResultID(ctx, dag, resultID, "remote git mirror", "bare_repo")
	if err != nil {
		return nil, err
	}
	query, err := persistedDecodeQuery(dag)
	if err != nil {
		return nil, err
	}
	ref, err := query.SnapshotManager().GetMutableBySnapshotID(ctx, link.RefKey, bkcache.NoUpdateLastUsed)
	if err != nil {
		return nil, fmt.Errorf("reopen persisted remote git mirror snapshot %q: %w", link.RefKey, err)
	}
	mirror.snapshot = ref
	return mirror, nil
}

func (mirror *RemoteGitMirror) EnsureCreated(ctx context.Context, query *Query) error {
	if mirror == nil {
		return fmt.Errorf("remote git mirror is nil")
	}
	mirror.mu.Lock()
	defer mirror.mu.Unlock()
	return mirror.ensureSnapshotLocked(ctx, query)
}

func (mirror *RemoteGitMirror) acquire(ctx context.Context, query *Query) (_ bkcache.MutableRef, release func(), err error) {
	if mirror == nil {
		return nil, nil, fmt.Errorf("remote git mirror is nil")
	}
	mirror.mu.Lock()
	if err := mirror.ensureSnapshotLocked(ctx, query); err != nil {
		mirror.mu.Unlock()
		return nil, nil, err
	}
	return mirror.snapshot, mirror.mu.Unlock, nil
}

func (mirror *RemoteGitMirror) ensureSnapshotLocked(ctx context.Context, query *Query) error {
	if mirror.snapshot != nil {
		return nil
	}
	ref, err := query.SnapshotManager().New(
		ctx,
		nil,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("git bare repo for %s", mirror.RemoteURL)),
	)
	if err != nil {
		return err
	}
	mirror.snapshot = ref
	return nil
}
