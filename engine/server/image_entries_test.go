package server

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
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
	return imageEntriesDirOf(t, "shipped")
}

// imageEntriesDirOf exports an evaluated file of a source engine for each
// name as an engine image's entries, and returns their directory and bundle.
func imageEntriesDirOf(t *testing.T, names ...string) (string, dagql.ValueBundle) {
	t.Helper()
	src := newAdapterTestEngine(t)
	var outputs []dagql.SelectedValueOutput
	for _, name := range names {
		file := src.file(t, name)
		require.NoError(t, src.cache.Evaluate(src.ctx, file))
		outputs = append(outputs, dagql.SelectedValueOutput{Result: file, Address: snapshotPart})
	}
	dir := t.TempDir()
	require.NoError(t, src.cache.ExportImageBundle(src.ctx, outputs, exportRefConfig, testImageDir{dir}))
	raw, err := os.ReadFile(filepath.Join(dir, schema.EngineImageBundleFile))
	require.NoError(t, err)
	var bundle dagql.ValueBundle
	require.NoError(t, json.Unmarshal(raw, &bundle))
	require.Len(t, bundle.Roots, len(names))
	require.NotEmpty(t, bundle.Outputs, "the files' parts are outputs")
	return dir, bundle
}

// expireImageRoot gives the image's root for the file name an expiry that has
// passed, on the root and on its value, as an image's build writes the expiry
// a module function's result got (a function's default lifetime is a week),
// and rewrites the image's bundle.
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

// An engine merges its image's entries at start, and the start never depends
// on them. An image without entries starts as before; an entry that has
// expired since the image was built is left out; a bundle that names a blob
// the image doesn't hold is left out whole.
func TestMergeImageEntries(t *testing.T) {
	t.Run("merges", func(t *testing.T) {
		dir, _ := imageEntriesDir(t)
		e := newAdapterTestEngine(t)
		srv := &Server{engineCache: e.cache}
		srv.mergeImageEntries(e.ctx, dir)
		require.True(t, e.cache.FromImage(e.file(t, "shipped")))
	})
	t.Run("no image entries", func(t *testing.T) {
		e := newAdapterTestEngine(t)
		srv := &Server{engineCache: e.cache}
		require.NoError(t, srv.mergeImageBundle(e.ctx, filepath.Join(t.TempDir(), "absent")))
		require.NoError(t, srv.mergeImageBundle(e.ctx, t.TempDir()))
	})
	t.Run("an expired entry is left out", func(t *testing.T) {
		dir, bundle := imageEntriesDirOf(t, "shipped", "lapsed")
		expireImageRoot(t, dir, bundle, "lapsed")
		e := newAdapterTestEngine(t)
		srv := &Server{engineCache: e.cache}
		require.NoError(t, srv.mergeImageBundle(e.ctx, dir))
		require.True(t, e.cache.FromImage(e.file(t, "shipped")), "the unexpired entry merged")
		require.False(t, e.cache.FromImage(e.file(t, "lapsed")), "the expired entry was left out")
	})
	t.Run("a missing blob leaves the entries out", func(t *testing.T) {
		dir, _ := imageEntriesDir(t)
		require.NoError(t, os.RemoveAll(filepath.Join(dir, "blobs")))
		e := newAdapterTestEngine(t)
		srv := &Server{engineCache: e.cache}
		require.ErrorContains(t, srv.mergeImageBundle(e.ctx, dir), "engine image entries: blob")
		srv.mergeImageEntries(e.ctx, dir)
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
