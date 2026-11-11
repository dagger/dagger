package server

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// testImageDir writes an engine image's bundle and blobs into a directory, as
// the image's build does.
type testImageDir struct{ dir string }

func (d testImageDir) WriteBundle(_ context.Context, bundle dagql.ValueBundle) error {
	raw, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.dir, schema.EngineImageBundleFile), raw, 0o644)
}

func (d testImageDir) WriteBlob(_ context.Context, dgst digest.Digest, r content.ReaderAt) error {
	name := filepath.Join(d.dir, dagql.ImageBlobPath(dgst))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	data, err := io.ReadAll(io.NewSectionReader(r, 0, r.Size()))
	if err != nil {
		return err
	}
	return os.WriteFile(name, data, 0o644)
}

// imageEntriesDir exports one evaluated file of a source engine as an engine
// image's entries, and returns their directory and bundle.
func imageEntriesDir(t *testing.T) (string, dagql.ValueBundle) {
	t.Helper()
	src := newAdapterTestEngine(t)
	shipped := src.file(t, "shipped")
	require.NoError(t, src.cache.Evaluate(src.ctx, shipped))
	dir := t.TempDir()
	require.NoError(t, src.cache.ExportImageBundle(src.ctx, []dagql.SelectedValueOutput{{Result: shipped, Address: snapshotPart}}, exportRefConfig, testImageDir{dir}))
	raw, err := os.ReadFile(filepath.Join(dir, schema.EngineImageBundleFile))
	require.NoError(t, err)
	var bundle dagql.ValueBundle
	require.NoError(t, json.Unmarshal(raw, &bundle))
	require.Len(t, bundle.Roots, 1)
	require.NotEmpty(t, bundle.Outputs, "the file's part is an output")
	return dir, bundle
}

// An engine merges its image's entries at start. An image without entries
// starts as before; one whose bundle names a blob the image doesn't hold
// fails the start.
func TestMergeImageEntries(t *testing.T) {
	t.Run("merges", func(t *testing.T) {
		dir, _ := imageEntriesDir(t)
		e := newAdapterTestEngine(t)
		srv := &Server{engineCache: e.cache}
		require.NoError(t, srv.mergeImageEntries(e.ctx, dir))
	})
	t.Run("no image entries", func(t *testing.T) {
		e := newAdapterTestEngine(t)
		srv := &Server{engineCache: e.cache}
		require.NoError(t, srv.mergeImageEntries(e.ctx, filepath.Join(t.TempDir(), "absent")))
		require.NoError(t, srv.mergeImageEntries(e.ctx, t.TempDir()))
	})
	t.Run("a missing blob fails the start", func(t *testing.T) {
		dir, _ := imageEntriesDir(t)
		require.NoError(t, os.RemoveAll(filepath.Join(dir, "blobs")))
		e := newAdapterTestEngine(t)
		srv := &Server{engineCache: e.cache}
		require.ErrorContains(t, srv.mergeImageEntries(e.ctx, dir), "engine image entries: blob")
	})
}

// An entry the engine image ships is never exported: the export refuses it as
// a root and leaves its part out, and exports the entries the engine made.
func TestRemoteCacheAdapterExportRefusesImageEntries(t *testing.T) {
	dir, bundle := imageEntriesDir(t)
	e := newAdapterTestEngine(t)
	require.NoError(t, e.cache.SetImageContent(dir))
	reply, err := e.cache.MergeValues(e.ctx, dagql.ImageCacheID, bundle)
	require.NoError(t, err)
	require.Len(t, reply.Roots, 1)
	shipped := reply.Roots[0].Number
	made := e.file(t, "made")
	require.NoError(t, e.cache.Evaluate(e.ctx, made))

	req := protocol.Export{
		Roots:   []uint64{shipped, entryNumber(t, made)},
		Outputs: []protocol.ExportOutput{{Number: shipped, Address: snapshotPart}, {Number: entryNumber(t, made), Address: snapshotPart}},
	}
	calls := 0
	err = e.adapter.Export(e.ctx, req, nil, func(_ context.Context, exp remotecache.Export) error {
		calls++
		require.Equal(t, []protocol.SkippedRoot{{Number: shipped, Reason: protocol.SkipRefused, Message: dagql.ErrFromImage.Error()}}, exp.Skipped)
		require.NotNil(t, exp.Bundle)
		require.Equal(t, []uint64{entryNumber(t, made)}, bundleRoots(exp.Bundle))
		require.Len(t, exp.Bundle.Outputs, 1, "only the made entry's part")
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

// A prune never deletes what the engine's image ships: after a prune of
// everything, a call of the image's root is a hit, with nothing recomputed,
// while a result the engine made itself is gone.
func TestPruneKeepsTheImagesEntries(t *testing.T) {
	dir, _ := imageEntriesDir(t)
	e := newAdapterTestEngine(t)
	srv := &Server{engineCache: e.cache}
	require.NoError(t, srv.mergeImageEntries(e.ctx, dir))
	made := e.file(t, "made")
	require.NoError(t, e.cache.Evaluate(e.ctx, made))
	require.NoError(t, e.cache.ReleaseSession(e.ctx, "adapter"))

	_, err := e.cache.Prune(e.ctx, []dagql.CachePrunePolicy{{All: true}})
	require.NoError(t, err)

	require.Equal(t, dagql.CacheOutcomeHit, callFile(t, e, "after-prune", "shipped").Outcome, "the image's root is still there")
	require.NotEqual(t, dagql.CacheOutcomeHit, callFile(t, e, "after-prune", "made").Outcome, "the prune deleted what the engine made")
}

// callFile calls the file field name, as publishFile publishes it, in
// session, and returns the call's cache decision.
func callFile(t *testing.T, e *adapterTestEngine, session, name string) *dagql.CacheDecision {
	t.Helper()
	file := &core.File{File: new(core.LazyAccessor[string, *core.File]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File]), Platform: adapterTestPlatform,
		Lazy: &core.FileBlobLazy{LazyState: core.NewLazyState(), Filename: name + ".txt", Contents: []byte(name + " bytes"), Permissions: 0o644}}
	file.SetPath("/pending")
	call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: name, Type: dagql.NewResultCallType(file.Type())}
	decision := dagql.NewCacheDecision()
	_, err := e.cache.GetOrInitCall(e.ctx, session, e.srv, &dagql.CallRequest{ResultCall: call, IsPersistable: true, CacheEvidence: decision}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(file, e.srv, call)
	})
	require.NoError(t, err)
	return decision
}

// An image that names built-in SDK files its entries don't hold fails the
// start.
func TestCheckImageDirectories(t *testing.T) {
	dir, _ := imageEntriesDir(t)
	e := newAdapterTestEngine(t)
	srv := &Server{engineCache: e.cache}
	require.NoError(t, srv.mergeImageEntries(e.ctx, dir))
	require.NoError(t, srv.checkImageDirectories([]string{""}))
	require.ErrorIs(t, srv.checkImageDirectories([]string{digest.FromString("absent").String()}), dagql.ErrUnknownEntry)
}
