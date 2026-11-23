package server

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
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

func (d testImageDir) WriteBlob(_ context.Context, dgst digest.Digest, r io.Reader) error {
	name := filepath.Join(d.dir, dagql.ImageBlobPath(dgst))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return os.WriteFile(name, data, 0o644)
}

// imageEntriesDir exports one evaluated file of a source engine as an engine
// image's entries, and returns their directory and bundle.
func imageEntriesDir(t *testing.T) (string, dagql.ValueBundle) {
	t.Helper()
	return imageEntriesDirOf(t, "shipped")
}

// imageEntriesDirOf exports an evaluated file of a source engine for each
// name as an engine image's entries, and returns their directory and bundle.
func imageEntriesDirOf(t *testing.T, names ...string) (string, dagql.ValueBundle) {
	t.Helper()
	return exportImageEntries(t, newAdapterTestEngine(t), exportRefConfig, nil, names...)
}

// imageRefConfig is the config the engine image's build exports its entries
// with (_engineImageBundle): uncompressed blobs.
var imageRefConfig = config.RefConfig{Compression: compression.New(compression.Uncompressed).SetForce(true)}

// exportImageEntries exports an evaluated file of src for each name as an
// engine image's entries, with cfg, and with their blobs' times clamped to
// epoch when it is set, and returns their directory and bundle.
func exportImageEntries(t *testing.T, src *adapterTestEngine, cfg config.RefConfig, epoch *time.Time, names ...string) (string, dagql.ValueBundle) {
	t.Helper()
	var outputs []dagql.SelectedValueOutput
	for _, name := range names {
		file := src.file(t, name)
		require.NoError(t, src.cache.Evaluate(src.ctx, file))
		outputs = append(outputs, dagql.SelectedValueOutput{Result: file, Address: snapshotPart})
	}
	dir := t.TempDir()
	require.NoError(t, src.cache.ExportImageBundle(src.ctx, outputs, cfg, epoch, testImageDir{dir}))
	raw, err := os.ReadFile(filepath.Join(dir, schema.EngineImageBundleFile))
	require.NoError(t, err)
	var bundle dagql.ValueBundle
	require.NoError(t, json.Unmarshal(raw, &bundle))
	require.Len(t, bundle.Roots, len(names))
	require.NotEmpty(t, bundle.Outputs, "the files' parts are outputs")
	return dir, bundle
}

// expireImageRoot gives the image's root for the file name an expiry that has
// passed, on the root and on its value, as an image's bundle holds once the
// expiry a module function's time to live gave its result has passed, and
// rewrites the image's bundle.
func expireImageRoot(t *testing.T, dir string, bundle dagql.ValueBundle, name string) {
	t.Helper()
	past := time.Now().Add(-time.Hour).Unix()
	var ordinal dagql.TransferOrdinal
	for i, value := range bundle.Values {
		if value.Record.Call != nil && value.Record.Call.Field == name {
			ordinal = value.Ordinal
			bundle.Values[i].ExpiresAtUnix = past
		}
	}
	require.NotZero(t, ordinal, "the bundle holds %s", name)
	expired := false
	for i, root := range bundle.Roots {
		if root.Ordinal == ordinal {
			bundle.Roots[i].ExpiresAtUnix = past
			expired = true
		}
	}
	require.True(t, expired, "%s is a root of the bundle", name)
	require.NoError(t, testImageDir{dir}.WriteBundle(t.Context(), bundle))
}

// imageBlobs are the blobs of the image's entries in dir, by path.
func imageBlobs(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	blobs := map[string][]byte{}
	root := filepath.Join(dir, "blobs")
	require.NoError(t, filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(name)
		blobs[strings.TrimPrefix(name, root+"/")] = data
		return err
	}))
	return blobs
}

// The image's entries exported with an epoch are their files dated at the
// epoch at the latest, so two engines that wrote the same file at different
// times ship the same blobs. The times are in no lookup's key: the bundle's
// records are the same as without the epoch, the bundle names each clamped
// blob by the digest of its bytes, and an engine that merges the clamped
// entries finds the entry. Installing a part from the image's blobs is
// TestImageOfferInstallsFromTheImage's, and every test engine's, whose image
// ships its entries clamped.
func TestImageEntriesClampedToTheEpoch(t *testing.T) {
	t.Parallel()
	epoch := time.Unix(315619200, 0).UTC()
	src := newAdapterTestEngine(t)
	dir, clamped := exportImageEntries(t, src, imageRefConfig, &epoch, "shipped")
	_, unclamped := exportImageEntries(t, src, imageRefConfig, nil, "shipped")
	require.Equal(t, unclamped.Roots, clamped.Roots)
	require.Equal(t, unclamped.Values, clamped.Values, "every record, and so every lookup's key, is the same")

	blobs := imageBlobs(t, dir)
	require.NotEmpty(t, blobs)
	for name, blob := range blobs {
		tr := tar.NewReader(bytes.NewReader(blob))
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			require.False(t, hdr.ModTime.After(epoch), "%s: %s is dated %s, after the epoch", name, hdr.Name, hdr.ModTime)
		}
	}

	// The same file, written at least a second later by another engine: a
	// layer's tar keeps times to the second.
	time.Sleep(1100 * time.Millisecond)
	laterDir, _ := exportImageEntries(t, newAdapterTestEngine(t), imageRefConfig, &epoch, "shipped")
	require.Equal(t, blobs, imageBlobs(t, laterDir), "the same files make the same blobs")

	for name, blob := range blobs {
		require.Equal(t, strings.Replace(name, "/", ":", 1), digest.FromBytes(blob).String(), "a blob is named by the digest of its clamped bytes")
	}
	var layers int
	for _, output := range clamped.Outputs {
		if output.Chain == nil {
			continue
		}
		for _, layer := range output.Chain.Layers {
			layers++
			d := layer.Descriptor.Digest
			blob, ok := blobs[path.Join(d.Algorithm().String(), d.Encoded())]
			require.True(t, ok, "the bundle names %s, a blob of the image", d)
			require.EqualValues(t, len(blob), layer.Descriptor.Size)
			require.Equal(t, d.String(), layer.Descriptor.Annotations[labels.LabelUncompressed], "an uncompressed blob's digest is its uncompressed digest")
			require.False(t, layer.CreatedAt != nil && layer.CreatedAt.After(epoch), "the layer is dated at the epoch at the latest")
		}
	}
	require.NotZero(t, layers)

	e := newAdapterTestEngine(t)
	srv := &Server{engineCache: e.cache}
	srv.mergeImageEntries(e.ctx, dir, nil)
	require.True(t, e.cache.FromImage(e.file(t, "shipped")), "the clamped entry is found")
}

// An engine merges its image's entries at start, and the start never depends
// on them. An image without entries starts as before; an entry whose expiry
// has passed since the image was built merges all the same, since the merge
// drops the expiries the image's build gave its entries; a bundle that names
// a blob the image doesn't hold is left out whole.
func TestMergeImageEntries(t *testing.T) {
	t.Run("merges", func(t *testing.T) {
		dir, _ := imageEntriesDir(t)
		e := newAdapterTestEngine(t)
		srv := &Server{engineCache: e.cache}
		srv.mergeImageEntries(e.ctx, dir, nil)
		require.True(t, e.cache.FromImage(e.file(t, "shipped")))
	})
	t.Run("no image entries", func(t *testing.T) {
		e := newAdapterTestEngine(t)
		srv := &Server{engineCache: e.cache}
		require.NoError(t, srv.mergeImageBundle(e.ctx, filepath.Join(t.TempDir(), "absent")))
		require.NoError(t, srv.mergeImageBundle(e.ctx, t.TempDir()))
	})
	t.Run("an entry whose expiry has passed merges", func(t *testing.T) {
		dir, bundle := imageEntriesDirOf(t, "shipped", "lapsed")
		expireImageRoot(t, dir, bundle, "lapsed")
		e := newAdapterTestEngine(t)
		srv := &Server{engineCache: e.cache}
		require.NoError(t, srv.mergeImageBundle(e.ctx, dir))
		require.True(t, e.cache.FromImage(e.file(t, "shipped")))
		// The lookup finds the image's entry: it merged, and it hasn't
		// expired since.
		require.True(t, e.cache.FromImage(e.file(t, "lapsed")), "the entry whose expiry passed merged")
	})
	t.Run("a missing blob leaves the entries out", func(t *testing.T) {
		dir, _ := imageEntriesDir(t)
		require.NoError(t, os.RemoveAll(filepath.Join(dir, "blobs")))
		e := newAdapterTestEngine(t)
		srv := &Server{engineCache: e.cache}
		require.ErrorContains(t, srv.mergeImageBundle(e.ctx, dir), "engine image entries: blob")
		srv.mergeImageEntries(e.ctx, dir, nil)
		require.False(t, e.cache.FromImage(e.file(t, "shipped")), "nothing merged")
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
	require.NoError(t, srv.mergeImageBundle(e.ctx, dir))
	made := e.file(t, "made")
	require.NoError(t, e.cache.Evaluate(e.ctx, made))
	require.NoError(t, e.cache.ReleaseSession(e.ctx, "adapter"))
	require.NoError(t, e.cache.WaitSessionRelease(e.ctx, "adapter"))

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

// sdkFiles publishes an empty directory with the content digest name's,
// as the built-in SDKs' files are directories the image's entries find by
// content digest.
func (e *adapterTestEngine) sdkFiles(t *testing.T, name string) (dagql.ObjectResult[*core.Directory], digest.Digest) {
	t.Helper()
	dir := &core.Directory{Platform: adapterTestPlatform, Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory]),
		Lazy: &core.DirectoryScratchLazy{LazyState: core.NewLazyState()}}
	dir.SetPath("/")
	call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: name, Type: dagql.NewResultCallType(dir.Type())}
	content := digest.FromString(name)
	res, err := e.cache.GetOrInitCall(e.ctx, "adapter", e.srv, &dagql.CallRequest{ResultCall: call, IsPersistable: true}, func(ctx context.Context) (dagql.AnyResult, error) {
		res, err := dagql.NewObjectResultForCall(dir, e.srv, call)
		if err != nil {
			return nil, err
		}
		return res.WithContentDigest(ctx, content)
	})
	require.NoError(t, err)
	return res.(dagql.ObjectResult[*core.Directory]), content
}

// sdkImageEntriesDir exports an engine image's entries holding a built-in
// SDK's files, as a directory with the content digest it returns, and a file
// named shipped, and returns their directory.
func sdkImageEntriesDir(t *testing.T) (string, digest.Digest) {
	t.Helper()
	src := newAdapterTestEngine(t)
	src.srv.InstallObject(dagql.NewClass(src.srv, dagql.ClassOpts[*core.Directory]{}))
	files, content := src.sdkFiles(t, "go-sdk")
	shipped := src.file(t, "shipped")
	for _, res := range []dagql.AnyResult{files, shipped} {
		require.NoError(t, src.cache.Evaluate(src.ctx, res))
	}
	dir := t.TempDir()
	outputs := []dagql.SelectedValueOutput{{Result: files, Address: snapshotPart}, {Result: shipped, Address: snapshotPart}}
	require.NoError(t, src.cache.ExportImageBundle(src.ctx, outputs, exportRefConfig, nil, testImageDir{dir}))
	return dir, content
}

// The built-in SDKs' files are found among the image's entries by content
// digest, and no module of those SDKs loads without them. When they aren't
// there, a lookup of them says why: the merge's own error, or an image whose
// entries hold no such directory.
func TestBuiltinSDKFilesSayWhyTheyAreMissing(t *testing.T) {
	newEngine := func(t *testing.T) (*adapterTestEngine, *Server) {
		e := newAdapterTestEngine(t)
		e.srv.InstallObject(dagql.NewClass(e.srv, dagql.ClassOpts[*core.Directory]{}))
		return e, &Server{engineCache: e.cache}
	}
	t.Run("everything merges", func(t *testing.T) {
		dir, content := sdkImageEntriesDir(t)
		e, srv := newEngine(t)
		srv.mergeImageEntries(e.ctx, dir, []digest.Digest{content, ""})
		require.Empty(t, srv.missingImageDirectories([]digest.Digest{content}))
		require.True(t, e.cache.FromImage(e.file(t, "shipped")))
	})
	t.Run("the bundle can't be read", func(t *testing.T) {
		dir, content := sdkImageEntriesDir(t)
		require.NoError(t, os.WriteFile(filepath.Join(dir, schema.EngineImageBundleFile), []byte("{"), 0o644))
		e, srv := newEngine(t)
		srv.mergeImageEntries(e.ctx, dir, []digest.Digest{content})
		_, err := e.cache.ImageEntryByContent(content, "Directory")
		require.ErrorIs(t, err, dagql.ErrUnknownEntry)
		require.ErrorContains(t, err, "engine image entries: unexpected end of JSON input", "the lookup names why")
	})
	t.Run("the image ships no entries", func(t *testing.T) {
		e, srv := newEngine(t)
		absent := filepath.Join(t.TempDir(), "entries")
		content := digest.FromString("go-sdk")
		srv.mergeImageEntries(e.ctx, absent, []digest.Digest{content})
		_, err := e.cache.ImageEntryByContent(content, "Directory")
		require.ErrorIs(t, err, dagql.ErrUnknownEntry)
		require.ErrorContains(t, err, "the engine image's entries at "+absent+" hold no such directory", "the lookup names why")
	})
}

// An engine image's entries are made on an engine busy with other work, whose
// own captures, such as the remote cache's, can hold a result the export
// needs. The export waits for a result that's busy now and exports every
// result once it's free, as on an idle engine. A result it refuses fails the
// export at once, and a wait ends with the export's context.
func TestExportImageBundleWaitsForBusyResults(t *testing.T) {
	export := func(e *adapterTestEngine, ctx context.Context, dir string, results ...dagql.AnyResult) <-chan error {
		var outputs []dagql.SelectedValueOutput
		for _, res := range results {
			outputs = append(outputs, dagql.SelectedValueOutput{Result: res, Address: snapshotPart})
		}
		exported := make(chan error, 1)
		go func() { exported <- e.cache.ExportImageBundle(ctx, outputs, exportRefConfig, nil, testImageDir{dir}) }()
		return exported
	}
	t.Run("busy", func(t *testing.T) {
		e := newAdapterTestEngine(t)
		ready := e.file(t, "ready")
		require.NoError(t, e.cache.Evaluate(e.ctx, ready))
		busy := e.busyFile(t, "busy")
		dir := t.TempDir()
		exported := export(e, e.ctx, dir, ready, busy)
		// Give the export time to find the result busy before its body ends.
		time.Sleep(200 * time.Millisecond)
		e.bodies.unpause()
		require.NoError(t, exportErr(t, exported))
		raw, err := os.ReadFile(filepath.Join(dir, schema.EngineImageBundleFile))
		require.NoError(t, err)
		var bundle dagql.ValueBundle
		require.NoError(t, json.Unmarshal(raw, &bundle))
		require.ElementsMatch(t, []uint64{entryNumber(t, ready), entryNumber(t, busy)}, bundleRoots(&bundle), "every result is exported")
		require.Len(t, bundle.Outputs, 2)
	})
	t.Run("refused", func(t *testing.T) {
		e := newAdapterTestEngine(t)
		ready := e.file(t, "ready")
		require.NoError(t, e.cache.Evaluate(e.ctx, ready))
		refused := e.refusedFile(t, "refused")
		err := exportErr(t, export(e, e.ctx, t.TempDir(), ready, refused))
		require.ErrorContains(t, err, "export image bundle: 1 of 2 results left out")
		require.NotErrorIs(t, err, dagql.ErrPersistStateNotReady)
	})
	t.Run("canceled", func(t *testing.T) {
		e := newAdapterTestEngine(t)
		busy := e.busyFile(t, "busy")
		ctx, cancel := context.WithCancel(e.ctx)
		exported := export(e, ctx, t.TempDir(), busy)
		time.Sleep(200 * time.Millisecond)
		cancel()
		require.ErrorIs(t, exportErr(t, exported), context.Canceled)
	})
}

// exportErr is the error an export sends on exported, within 10 seconds.
func exportErr(t *testing.T, exported <-chan error) error {
	t.Helper()
	select {
	case err := <-exported:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the export")
		return nil
	}
}
