package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	cerrdefs "github.com/containerd/errdefs"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
)

type RemoteGitMirror struct {
	RemoteURL string

	mu       sync.Mutex
	snapshot bkcache.MutableRef

	snapshotID string

	persistedResultID uint64
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

func (mirror *RemoteGitMirror) PersistedResultID() uint64 {
	if mirror == nil {
		return 0
	}
	return mirror.persistedResultID
}

func (mirror *RemoteGitMirror) SetPersistedResultID(resultID uint64) {
	if mirror != nil {
		mirror.persistedResultID = resultID
	}
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
	snapshotID := mirror.snapshotID
	if snapshotID == "" && mirror.snapshot != nil {
		snapshotID = mirror.snapshot.SnapshotID()
	}
	if snapshotID == "" {
		return nil
	}
	return []dagql.PersistedSnapshotRefLink{{
		RefKey: snapshotID,
		Role:   "bare_repo",
	}}
}

func (mirror *RemoteGitMirror) CacheUsageMayChange() bool {
	return true
}

func (mirror *RemoteGitMirror) CacheUsageIdentity() (string, bool) {
	if mirror == nil || mirror.snapshotID == "" {
		return "", false
	}
	return mirror.snapshotID, true
}

func (mirror *RemoteGitMirror) CacheUsageSize(ctx context.Context) (int64, bool, error) {
	if mirror == nil || mirror.snapshotID == "" {
		return 0, false, nil
	}
	mirror.mu.Lock()
	snapshot := mirror.snapshot
	snapshotID := mirror.snapshotID
	mirror.mu.Unlock()
	if snapshot != nil {
		size, err := snapshot.Size(ctx)
		if err != nil {
			return 0, false, err
		}
		return size, true, nil
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return 0, false, err
	}
	ref, err := query.SnapshotManager().GetBySnapshotID(ctx, snapshotID, bkcache.NoUpdateLastUsed)
	if err != nil {
		return 0, false, err
	}
	defer func() {
		_ = ref.Release(context.WithoutCancel(ctx))
	}()
	size, err := ref.Size(ctx)
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
	if resultID != 0 {
		links, err := loadPersistedSnapshotLinksByResultID(ctx, dag, resultID, "remote git mirror")
		if err != nil {
			return nil, err
		}
		for _, link := range links {
			if link.Role != "bare_repo" {
				continue
			}
			mirror.snapshotID = link.RefKey
			break
		}
	}
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
	if mirror.snapshotID != "" {
		ref, err := query.SnapshotManager().GetMutableBySnapshotID(ctx, mirror.snapshotID, bkcache.NoUpdateLastUsed)
		if err == nil {
			mirror.snapshot = ref
			return nil
		}
		if !cerrdefs.IsNotFound(err) {
			return err
		}
		mirror.snapshotID = ""
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
	mirror.snapshotID = ref.SnapshotID()
	return nil
}
