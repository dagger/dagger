package snapshots_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/pkg/labels"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// tarEntry is one entry of a hand-written layer: a directory (name ending in
// "/"), a symlink (link set) or a regular file, whiteouts included.
type tarEntry struct {
	name string
	mode int64
	body string
	link string
}

// tarChain writes each layer as a gzipped tar blob to store's content and
// returns the chain of them, with store's content as its provider.
func tarChain(t *testing.T, store *testutil.Store, layers ...[]tarEntry) *bkcache.ExportChain {
	t.Helper()
	ctx := context.Background()
	chain := &bkcache.ExportChain{Provider: store.Content}
	for i, entries := range layers {
		var raw bytes.Buffer
		tw := tar.NewWriter(&raw)
		for _, e := range entries {
			hdr := &tar.Header{Name: e.name, Mode: e.mode, ModTime: testutil.FileTime, Typeflag: tar.TypeReg, Size: int64(len(e.body))}
			switch {
			case e.link != "":
				hdr.Typeflag, hdr.Linkname, hdr.Size = tar.TypeSymlink, e.link, 0
			case e.name[len(e.name)-1] == '/':
				hdr.Typeflag, hdr.Size = tar.TypeDir, 0
			}
			require.NoError(t, tw.WriteHeader(hdr))
			_, err := tw.Write([]byte(e.body))
			require.NoError(t, err)
		}
		require.NoError(t, tw.Close())
		var blob bytes.Buffer
		gz := gzip.NewWriter(&blob)
		_, err := gz.Write(raw.Bytes())
		require.NoError(t, err)
		require.NoError(t, gz.Close())
		desc := ocispecs.Descriptor{
			MediaType:   ocispecs.MediaTypeImageLayerGzip,
			Digest:      digest.FromBytes(blob.Bytes()),
			Size:        int64(blob.Len()),
			Annotations: map[string]string{labels.LabelUncompressed: digest.FromBytes(raw.Bytes()).String()},
		}
		require.NoError(t, content.WriteBlob(ctx, store.Content, fmt.Sprintf("tar-layer-%d", i), bytes.NewReader(blob.Bytes()), desc))
		chain.Layers = append(chain.Layers, bkcache.ExportLayer{Descriptor: desc})
	}
	return chain
}

// requireParents checks that ref's snapshot has n layers: itself and its
// parents.
func requireParents(t *testing.T, store *testutil.Store, ref bkcache.ImmutableRef, n int) {
	t.Helper()
	depth := 0
	for id := ref.SnapshotID(); id != ""; depth++ {
		var err error
		id, err = store.Manager.SnapshotParent(context.Background(), id)
		require.NoError(t, err)
	}
	require.Equal(t, n, depth, "one snapshot per layer, each on its parent")
}

// On overlayfs a chain's layers apply at once: every layer's apply is in
// progress together, which an applier that lets none finish until all have
// started checks. The tree is the chain's, one snapshot per layer.
func TestImportChainOverlayAppliesLayersAtOnce(t *testing.T) {
	t.Parallel()
	const layers = 4
	producer, consumer := testutil.NewStore(t), testutil.NewOverlayStore(t)
	chain := layeredChain(t, producer, "layer", layers)
	var entered atomic.Int64
	all := make(chan struct{})
	consumer.BeforeApply = func(ctx context.Context, _ ocispecs.Descriptor) error {
		if entered.Add(1) == layers {
			close(all)
		}
		select {
		case <-all:
			return nil
		case <-time.After(10 * time.Second):
			return errors.New("the chain's layers did not all apply at once")
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	ref := importChain(t, consumer, supplied(chain, chain.Provider))
	want := map[string]string{}
	for i := range layers {
		want[fmt.Sprintf("layer-%02d.txt", i)] = fmt.Sprintf("file 640 layer %d", i)
	}
	require.Equal(t, want, testutil.OverlayTree(t, ref))
	requireParents(t, consumer, ref, layers)
	require.EqualValues(t, layers, consumer.Applies.Load())
}

// A chain whose blobs arrive by Await applies each layer as soon as its own
// blob is in: a later layer applies while an earlier one's blob is still
// awaited. The tree is the chain's, one snapshot per layer.
func TestImportChainOverlayAppliesEachLayerAsItsBlobArrives(t *testing.T) {
	t.Parallel()
	producer, consumer := testutil.NewStore(t), testutil.NewOverlayStore(t)
	chain := layeredChain(t, producer, "layer", 2)
	first, last := chain.Layers[0].Descriptor, chain.Layers[1].Descriptor
	lastApplied := make(chan struct{})
	consumer.BeforeApply = func(_ context.Context, desc ocispecs.Descriptor) error {
		if desc.Digest == last.Digest {
			close(lastApplied)
		}
		return nil
	}
	await := func(ctx context.Context, desc ocispecs.Descriptor) error {
		if desc.Digest == first.Digest {
			select {
			case <-lastApplied:
			case <-time.After(10 * time.Second):
				return errors.New("the last layer did not apply while the first one's blob was awaited")
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		data, err := content.ReadBlob(ctx, chain.Provider, desc)
		if err != nil {
			return err
		}
		return content.WriteBlob(ctx, consumer.Content, "awaited-"+desc.Digest.Encoded(), bytes.NewReader(data), desc)
	}
	ref := importChain(t, consumer, &bkcache.ExportChain{Layers: chain.Layers, Await: await})
	require.Equal(t, map[string]string{"layer-00.txt": "file 640 layer 0", "layer-01.txt": "file 640 layer 1"}, testutil.OverlayTree(t, ref))
	requireParents(t, consumer, ref, 2)
	require.EqualValues(t, 2, consumer.Applies.Load())
}

// Layers applied apart give the tree overlayfs shows for the chain applied
// in order: a whiteout hides a file and a directory of the layers below it,
// and an opaque directory everything below it; a file replaced keeps the
// upper layer's bytes and mode.
func TestImportChainOverlayTree(t *testing.T) {
	t.Parallel()
	base := []tarEntry{
		{name: "a/", mode: 0o755},
		{name: "a/keep.txt", mode: 0o644, body: "keep"},
		{name: "a/gone.txt", mode: 0o644, body: "gone"},
		{name: "d/", mode: 0o755},
		{name: "d/old.txt", mode: 0o644, body: "old"},
		{name: "d/sub/", mode: 0o755},
		{name: "d/sub/x", mode: 0o644, body: "x"},
		{name: "r/", mode: 0o755},
		{name: "r/y", mode: 0o644, body: "y"},
		{name: "top.txt", mode: 0o644, body: "v1"},
		{name: "link", link: "a/keep.txt"},
	}
	whiteouts := []tarEntry{
		{name: "a/", mode: 0o755},
		{name: "a/.wh.gone.txt"},
		{name: ".wh.r"},
		{name: "top.txt", mode: 0o600, body: "v2"},
		{name: "n/", mode: 0o750},
		{name: "n/new.txt", mode: 0o644, body: "new"},
	}
	opaque := []tarEntry{
		{name: "d/", mode: 0o755},
		{name: "d/.wh..wh..opq"},
		{name: "d/fresh.txt", mode: 0o644, body: "fresh"},
	}
	added := []tarEntry{
		{name: "a/", mode: 0o755},
		{name: "a/added.txt", mode: 0o644, body: "added"},
	}
	want := map[string]string{
		"a":           "dir 755",
		"a/keep.txt":  "file 644 keep",
		"a/added.txt": "file 644 added",
		"d":           "dir 755",
		"d/old.txt":   "file 644 old",
		"d/sub":       "dir 755",
		"d/sub/x":     "file 644 x",
		"top.txt":     "file 600 v2",
		"link":        "symlink a/keep.txt",
		"n":           "dir 750",
		"n/new.txt":   "file 644 new",
	}
	t.Run("whiteouts", func(t *testing.T) {
		t.Parallel()
		producer, consumer := testutil.NewStore(t), testutil.NewOverlayStore(t)
		if err := consumer.CanMakeWhiteouts(); err != nil {
			t.Skipf("the store can't hold overlay whiteouts, which are device nodes: %v", err)
		}
		ref := importChain(t, consumer, tarChain(t, producer, base, whiteouts, added))
		require.Equal(t, want, testutil.OverlayTree(t, ref))
		requireParents(t, consumer, ref, 3)
	})
	t.Run("opaque directory", func(t *testing.T) {
		t.Parallel()
		producer, consumer := testutil.NewStore(t), testutil.NewOverlayStore(t)
		if err := consumer.CanMakeWhiteouts(); err != nil {
			t.Skipf("the store can't hold overlay whiteouts, which are device nodes: %v", err)
		}
		if err := consumer.CanMakeOpaque(); err != nil {
			t.Skipf("an opaque directory is marked by a trusted xattr, which takes CAP_SYS_ADMIN: %v", err)
		}
		ref := importChain(t, consumer, tarChain(t, producer, base, whiteouts, opaque, added))
		want := wantWithout(want, "d/")
		want["d/fresh.txt"] = "file 644 fresh"
		require.Equal(t, want, testutil.OverlayTree(t, ref))
		requireParents(t, consumer, ref, 4)
	})
}

// Two imports of chains that share their first layers, at once, apply each
// shared layer once: the one applying it holds the other off until it is
// committed, which the other then reuses.
func TestImportChainOverlaySharedLayersApplyOnce(t *testing.T) {
	t.Parallel()
	const shared = 4
	producer, consumer := testutil.NewStore(t), testutil.NewOverlayStore(t)
	var top bkcache.ImmutableRef
	for i := range shared {
		top, _ = producer.Build(t, top, fmt.Sprintf("shared-%d.txt", i), fmt.Sprintf("shared %d", i))
	}
	longer, _ := producer.Build(t, top, "own.txt", "own")
	chainA, chainB := exportChain(t, top), exportChain(t, longer)
	own := chainB.Layers[shared].Descriptor.Digest
	// No shared layer finishes applying until B's own layer has started:
	// B is importing by then, so both imports want the shared layers at
	// once.
	ownStarted := make(chan struct{})
	consumer.BeforeApply = func(ctx context.Context, desc ocispecs.Descriptor) error {
		if desc.Digest == own {
			close(ownStarted)
			return nil
		}
		select {
		case <-ownStarted:
			return nil
		case <-time.After(10 * time.Second):
			return errors.New("the other import never applied its own layer")
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	var wg sync.WaitGroup
	refs := make([]bkcache.ImmutableRef, 2)
	errs := make([]error, 2)
	for i, chain := range []*bkcache.ExportChain{chainA, chainB} {
		wg.Go(func() {
			refs[i], errs[i] = consumer.Manager.ImportChain(context.Background(), supplied(chain, chain.Provider))
		})
	}
	wg.Wait()
	for i := range refs {
		require.NoError(t, errs[i])
		t.Cleanup(func() { require.NoError(t, refs[i].Release(context.Background())) })
	}
	require.EqualValues(t, shared+1, consumer.Applies.Load(), "each layer applied once")
	want := map[string]string{}
	for i := range shared {
		want[fmt.Sprintf("shared-%d.txt", i)] = fmt.Sprintf("file 640 shared %d", i)
	}
	require.Equal(t, want, testutil.OverlayTree(t, refs[0]))
	want["own.txt"] = "file 640 own"
	require.Equal(t, want, testutil.OverlayTree(t, refs[1]))
	requireParents(t, consumer, refs[1], shared+1)
}

// An import whose layer fails to apply while the others apply leaves no
// lease or partial content behind, and nothing holds the chain's layers: a
// later import of the chain succeeds, and once it is released nothing is left
// uncommitted.
func TestImportChainOverlayFailureReleasesEverything(t *testing.T) {
	t.Parallel()
	const layers, failing = 6, 2
	producer, consumer := testutil.NewStore(t), testutil.NewOverlayStore(t)
	chain := layeredChain(t, producer, "layer", layers)
	errApply := errors.New("injected apply failure")
	consumer.BeforeApply = func(_ context.Context, desc ocispecs.Descriptor) error {
		if desc.Digest == chain.Layers[failing].Descriptor.Digest {
			return errApply
		}
		return nil
	}
	ref, err := consumer.Manager.ImportChain(context.Background(), supplied(chain, chain.Provider))
	require.Nil(t, ref)
	require.ErrorIs(t, err, errApply)
	owners, err := consumer.Leases.List(context.Background())
	require.NoError(t, err)
	require.Empty(t, owners)
	writes, err := consumer.Content.ListStatuses(context.Background())
	require.NoError(t, err)
	require.Empty(t, writes)

	consumer.BeforeApply = nil
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ref, err = consumer.Manager.ImportChain(ctx, supplied(chain, chain.Provider))
	require.NoError(t, err)
	want := map[string]string{}
	for i := range layers {
		want[fmt.Sprintf("layer-%02d.txt", i)] = fmt.Sprintf("file 640 layer %d", i)
	}
	require.Equal(t, want, testutil.OverlayTree(t, ref))
	requireParents(t, consumer, ref, layers)
	require.NoError(t, ref.Release(context.Background()))
	consumer.GC(t)
	assertNoTemporaryResources(t, consumer)
}

// wantWithout returns tree without path's entries below dir.
func wantWithout(tree map[string]string, dir string) map[string]string {
	out := map[string]string{}
	for path, entry := range tree {
		if len(path) < len(dir) || path[:len(dir)] != dir {
			out[path] = entry
		}
	}
	return out
}
