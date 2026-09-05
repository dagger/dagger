// Package testutil builds real local snapshot and content stores for transfer tests.
package testutil

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/diff/apply"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/mount"
	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/containerd/v2/plugins/diff/walking"
	"github.com/containerd/containerd/v2/plugins/snapshots/native"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	containerdsnapshot "github.com/dagger/dagger/engine/snapshots/containerd"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

const Namespace = "snapshot-transfer-test"

type Store struct {
	Manager     bkcache.SnapshotManager
	Content     *containerdsnapshot.Store
	Snapshots   bkcache.Snapshotter
	Leases      leases.Manager
	DB          *metadata.DB
	Applies     atomic.Int64
	Diffs       atomic.Int64
	BeforeApply func(context.Context, ocispecs.Descriptor) error
	BeforeDiff  func(context.Context) error
	BeforeAdd   func(context.Context, leases.Lease, leases.Resource) error
	AfterAdd    func(context.Context, leases.Lease, leases.Resource)
	AfterCreate func(leases.Lease)
	root        string
}

func NewStore(t testing.TB) *Store {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("real native snapshot transfer requires mount privileges")
	}
	s := &Store{root: t.TempDir()}
	rawContent, err := local.NewStore(filepath.Join(s.root, "content"))
	require.NoError(t, err)
	rawSnapshots, err := native.NewSnapshotter(filepath.Join(s.root, "snapshots"))
	require.NoError(t, err)
	db, err := bolt.Open(filepath.Join(s.root, "metadata.db"), 0600, nil)
	require.NoError(t, err)
	s.DB = metadata.NewDB(db, rawContent, map[string]ctdsnapshots.Snapshotter{"native": rawSnapshots})
	require.NoError(t, s.DB.Init(context.Background()))
	s.Leases = bkcache.NewLeaseManager(metadata.NewLeaseManager(s.DB), Namespace)
	s.Content = containerdsnapshot.NewContentStore(s.DB.ContentStore(), Namespace)
	s.Snapshots = containerdsnapshot.NewSnapshotter("native", s.DB.Snapshotter("native"), Namespace)
	s.openManager(t)
	t.Cleanup(func() {
		require.NoError(t, s.Manager.Close())
		require.NoError(t, rawSnapshots.Close())
		require.NoError(t, db.Close())
	})
	return s
}

func (s *Store) openManager(t testing.TB) {
	t.Helper()
	var err error
	s.Manager, err = bkcache.NewSnapshotManager(bkcache.SnapshotManagerOpt{
		Snapshotter: s.Snapshots, ContentStore: s.Content,
		LeaseManager:  &observedLeases{Manager: s.Leases, store: s},
		Applier:       &observedApplier{Applier: apply.NewFileSystemApplier(s.Content), store: s},
		Differ:        &observedDiffer{Comparer: walking.NewWalkingDiff(s.Content), store: s},
		MountPoolRoot: filepath.Join(s.root, "mounts"),
	})
	require.NoError(t, err)
}

func (s *Store) Reload(t testing.TB) {
	t.Helper()
	rows := s.Manager.PersistentMetadataRows()
	require.NoError(t, s.Manager.Close())
	s.openManager(t)
	require.NoError(t, s.Manager.LoadPersistentMetadata(rows))
}

func (s *Store) GC(t testing.TB) {
	t.Helper()
	_, err := s.DB.GarbageCollect(namespaces.WithNamespace(context.Background(), Namespace))
	require.NoError(t, err)
}

// Build writes through an ordinary mutable ref, commits, and keeps an ordinary
// owner lease. The caller can release that owner independently of returned refs.
func (s *Store) Build(t testing.TB, parent bkcache.ImmutableRef, name, value string) (bkcache.ImmutableRef, string) {
	t.Helper()
	ctx := context.Background()
	owner, err := s.Leases.Create(ctx, leases.WithRandomID())
	require.NoError(t, err)
	ctx = leases.WithLease(ctx, owner.ID)
	mut, err := s.Manager.New(ctx, parent)
	require.NoError(t, err)
	mounted, err := mut.Mount(ctx, false)
	require.NoError(t, err)
	mounter := bkcache.LocalMounter(mounted)
	root, err := mounter.Mount()
	require.NoError(t, err)
	if name != "" {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(value), 0640))
		require.NoError(t, os.Chtimes(path, FileTime, FileTime))
	}
	require.NoError(t, mounter.Unmount())
	var ref bkcache.ImmutableRef
	if name == "" {
		// No filesystem changes were made. Exercise the ordinary commit path
		// with known empty usage, as snapshot diff application can supply.
		ref, err = mut.CommitWithUsage(ctx, ctdsnapshots.Usage{})
	} else {
		ref, err = mut.Commit(ctx)
	}
	require.NoError(t, err)
	require.NoError(t, s.Manager.AttachLease(ctx, owner.ID, ref.SnapshotID()))
	t.Cleanup(func() {
		require.NoError(t, ref.Release(context.Background()))
		require.NoError(t, s.Manager.RemoveLease(context.Background(), owner.ID))
	})
	return ref, owner.ID
}

var FileTime = time.Unix(1700000000, 0).UTC()

func CheckFile(t testing.TB, ref bkcache.ImmutableRef, name, want string) {
	t.Helper()
	mounted, err := ref.Mount(context.Background(), true)
	require.NoError(t, err)
	mounter := bkcache.LocalMounter(mounted)
	root, err := mounter.Mount()
	require.NoError(t, err)
	defer func() { require.NoError(t, mounter.Unmount()) }()
	data, err := os.ReadFile(filepath.Join(root, name))
	require.NoError(t, err)
	require.Equal(t, want, string(data))
	info, err := os.Stat(filepath.Join(root, name))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0640), info.Mode().Perm())
	require.Equal(t, FileTime, info.ModTime().UTC())
}

type Provider struct {
	content.InfoReaderProvider
	Reads      atomic.Int64
	BeforeRead func(context.Context, ocispecs.Descriptor) error
}

func (p *Provider) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	p.Reads.Add(1)
	if p.BeforeRead != nil {
		if err := p.BeforeRead(ctx, desc); err != nil {
			return nil, err
		}
	}
	return p.InfoReaderProvider.ReaderAt(ctx, desc)
}

type observedApplier struct {
	diff.Applier
	store *Store
}

func (a *observedApplier) Apply(ctx context.Context, desc ocispecs.Descriptor, mounts []mount.Mount, opts ...diff.ApplyOpt) (ocispecs.Descriptor, error) {
	a.store.Applies.Add(1)
	if a.store.BeforeApply != nil {
		if err := a.store.BeforeApply(ctx, desc); err != nil {
			return ocispecs.Descriptor{}, err
		}
	}
	return a.Applier.Apply(ctx, desc, mounts, opts...)
}

type observedDiffer struct {
	diff.Comparer
	store *Store
}

func (d *observedDiffer) Compare(ctx context.Context, lower, upper []mount.Mount, opts ...diff.Opt) (ocispecs.Descriptor, error) {
	d.store.Diffs.Add(1)
	if d.store.BeforeDiff != nil {
		if err := d.store.BeforeDiff(ctx); err != nil {
			return ocispecs.Descriptor{}, err
		}
	}
	return d.Comparer.Compare(ctx, lower, upper, opts...)
}

type observedLeases struct {
	leases.Manager
	store *Store
}

func (l *observedLeases) AddResource(ctx context.Context, lease leases.Lease, resource leases.Resource) error {
	if l.store.BeforeAdd != nil {
		if err := l.store.BeforeAdd(ctx, lease, resource); err != nil {
			return err
		}
	}
	if err := l.Manager.AddResource(ctx, lease, resource); err != nil {
		return err
	}
	if l.store.AfterAdd != nil {
		l.store.AfterAdd(ctx, lease, resource)
	}
	return nil
}

func (l *observedLeases) Create(ctx context.Context, opts ...leases.Opt) (leases.Lease, error) {
	lease, err := l.Manager.Create(ctx, opts...)
	if err == nil && l.store.AfterCreate != nil {
		l.store.AfterCreate(lease)
	}
	return lease, err
}
