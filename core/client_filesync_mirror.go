package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/engine/filesync"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	snapshot "github.com/dagger/dagger/engine/snapshots/snapshotter"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
	digest "github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/grpc"

	"github.com/dagger/dagger/dagql"
)

type ClientFilesyncMirror struct {
	StableClientID string
	Drive          string
	EphemeralID    string

	mu sync.Mutex

	snapshotID string
	snapshot   bkcache.MutableRef

	mounter snapshot.Mounter
	mntPath string

	sharedState *filesync.MirrorSharedState
	usageCount  int

	persistedResultID uint64
}

var _ dagql.PersistedObject = (*ClientFilesyncMirror)(nil)
var _ dagql.PersistedObjectDecoder = (*ClientFilesyncMirror)(nil)
var _ dagql.OnReleaser = (*ClientFilesyncMirror)(nil)

func (*ClientFilesyncMirror) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ClientFilesyncMirror",
		NonNull:   true,
	}
}

func (*ClientFilesyncMirror) TypeDescription() string {
	return "An internal persistent filesync mirror."
}

func (m *ClientFilesyncMirror) PersistedResultID() uint64 {
	if m == nil {
		return 0
	}
	return m.persistedResultID
}

func (m *ClientFilesyncMirror) SetPersistedResultID(resultID uint64) {
	if m != nil {
		m.persistedResultID = resultID
	}
}

func (m *ClientFilesyncMirror) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
	if m == nil || m.snapshotID == "" {
		return nil
	}
	return []dagql.PersistedSnapshotRefLink{{
		RefKey: m.snapshotID,
		Role:   "snapshot",
		Slot:   "/",
	}}
}

func (m *ClientFilesyncMirror) CacheUsageMayChange() bool {
	return true
}

func (m *ClientFilesyncMirror) CacheUsageIdentities() []string {
	if m == nil || m.snapshotID == "" {
		return nil
	}
	return []string{m.snapshotID}
}

func (m *ClientFilesyncMirror) CacheUsageSize(ctx context.Context, identity string) (int64, bool, error) {
	if m == nil || m.snapshotID == "" || m.snapshotID != identity {
		return 0, false, nil
	}
	m.mu.Lock()
	snapshot := m.snapshot
	snapshotID := m.snapshotID
	m.mu.Unlock()
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

type persistedClientFilesyncMirrorPayload struct {
	StableClientID string `json:"stableClientID"`
	Drive          string `json:"drive,omitempty"`
}

func (m *ClientFilesyncMirror) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	_ = cache
	if m == nil {
		return nil, fmt.Errorf("encode persisted client filesync mirror: nil mirror")
	}
	if m.StableClientID == "" {
		return nil, fmt.Errorf("encode persisted client filesync mirror: stable client id is empty")
	}
	return json.Marshal(persistedClientFilesyncMirrorPayload{
		StableClientID: m.StableClientID,
		Drive:          m.Drive,
	})
}

func (*ClientFilesyncMirror) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedClientFilesyncMirrorPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted client filesync mirror payload: %w", err)
	}
	mirror := &ClientFilesyncMirror{
		StableClientID: persisted.StableClientID,
		Drive:          persisted.Drive,
	}
	if resultID != 0 {
		links, err := loadPersistedSnapshotLinksByResultID(ctx, dag, resultID, "client filesync mirror")
		if err != nil {
			return nil, err
		}
		for _, link := range links {
			if link.Role != "snapshot" {
				continue
			}
			mirror.snapshotID = link.RefKey
			break
		}
	}
	return mirror, nil
}

func (m *ClientFilesyncMirror) OnRelease(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usageCount = 0
	return m.releaseRuntimeLocked(ctx)
}

func (m *ClientFilesyncMirror) EnsureCreated(ctx context.Context, query *Query) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snapshotID != "" || m.snapshot != nil {
		return nil
	}
	ref, err := query.SnapshotManager().New(
		ctx,
		nil,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeLocalSource),
		bkcache.WithDescription(func() string {
			if m.StableClientID != "" {
				return fmt.Sprintf("client filesync mirror for %s%s", m.Drive, m.StableClientID)
			}
			return fmt.Sprintf("ephemeral client filesync mirror for %s%s", m.Drive, m.EphemeralID)
		}()),
	)
	if err != nil {
		return err
	}
	m.snapshot = ref
	m.snapshotID = ref.SnapshotID()
	return nil
}

func (m *ClientFilesyncMirror) Snapshot(
	ctx context.Context,
	query *Query,
	callerConn *grpc.ClientConn,
	clientPath string,
	opts filesync.SnapshotOpts,
) (bkcache.ImmutableRef, digest.Digest, error) {
	sharedState, release, err := m.acquire(ctx, query)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		_ = release(context.WithoutCancel(ctx))
	}()
	return filesync.NewFileSyncer(filesync.FileSyncerOpt{
		CacheAccessor: query.SnapshotManager(),
	}).Snapshot(ctx, sharedState, callerConn, clientPath, opts)
}

func (m *ClientFilesyncMirror) acquire(ctx context.Context, query *Query) (_ *filesync.MirrorSharedState, release func(context.Context) error, err error) {
	m.mu.Lock()
	if err := m.ensureRuntimeLocked(ctx, query); err != nil {
		m.mu.Unlock()
		return nil, nil, err
	}
	m.usageCount++
	sharedState := m.sharedState
	m.mu.Unlock()
	return sharedState, func(releaseCtx context.Context) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.usageCount--
		if m.usageCount > 0 {
			return nil
		}
		return m.releaseRuntimeLocked(releaseCtx)
	}, nil
}

func (m *ClientFilesyncMirror) ensureRuntimeLocked(ctx context.Context, query *Query) error {
	if m.sharedState != nil {
		return nil
	}
	if m.snapshot == nil {
		if m.snapshotID != "" {
			ref, err := query.SnapshotManager().GetMutableBySnapshotID(ctx, m.snapshotID, bkcache.NoUpdateLastUsed)
			if err == nil {
				m.snapshot = ref
			} else if !cerrdefs.IsNotFound(err) {
				return err
			} else {
				m.snapshotID = ""
			}
		}
		if m.snapshot == nil {
			if err := m.EnsureCreated(ctx, query); err != nil {
				return err
			}
		}
	}

	mountable, err := m.snapshot.Mount(ctx, false)
	if err != nil {
		return err
	}
	m.mounter = snapshot.LocalMounter(mountable)
	m.mntPath, err = m.mounter.Mount()
	if err != nil {
		return err
	}
	m.sharedState = filesync.NewMirrorSharedState(m.mntPath)
	return nil
}

func (m *ClientFilesyncMirror) releaseRuntimeLocked(ctx context.Context) (rerr error) {
	if m.mounter != nil {
		rerr = errorsJoin(rerr, m.mounter.Unmount())
		m.mounter = nil
	}
	m.mntPath = ""
	m.sharedState = nil
	if m.snapshot != nil {
		rerr = errorsJoin(rerr, m.snapshot.Release(context.WithoutCancel(ctx)))
		m.snapshot = nil
	}
	return rerr
}

func errorsJoin(errs ...error) error {
	var out error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if out == nil {
			out = err
		} else {
			out = fmt.Errorf("%w; %w", out, err)
		}
	}
	return out
}

func NewEphemeralClientFilesyncMirror(drive string) *ClientFilesyncMirror {
	return &ClientFilesyncMirror{
		Drive:       drive,
		EphemeralID: identity.NewID(),
	}
}
