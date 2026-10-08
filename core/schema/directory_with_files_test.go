package schema

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
)

// Directory.withFiles copies all its sources in one operation on its receiver,
// not in a chain of one withFile result per source, and saves that operation
// as its recipe. The copy itself is checked against a withFile chain in
// core/integration, since it needs snapshot mounts.
func TestDirectoryWithFilesIsOneOperation(t *testing.T) {
	store := testutil.NewStore(t)
	ctx, cache, srv := scratchTestCache(t, store, "", "with-files")
	srv.InstallObject(dagql.NewClass[*core.File](srv))
	ds := &directorySchema{}
	dagql.Fields[*core.Directory]{
		dagql.NodeFunc("withFile", ds.withFile).IsPersistable(),
		dagql.NodeFunc("withFiles", ds.withFiles).IsPersistable(),
	}.Install(srv)
	// Stored files, so that reading their paths opens nothing.
	dagql.Fields[*core.Query]{dagql.Func("savedFile", func(_ context.Context, _ *core.Query, args struct{ Path string }) (*core.File, error) {
		ref, _ := store.Build(t, nil, args.Path, "content of "+args.Path)
		file := &core.File{Platform: core.Platform{OS: "linux", Architecture: "arm64"}, File: new(core.LazyAccessor[string, *core.File]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File])}
		file.SetPath("/" + args.Path)
		file.SetSnapshot(ref)
		return file, nil
	})}.Install(srv)

	rowID := func(res dagql.AnyResult) uint64 {
		t.Helper()
		rec, err := cache.CapturePersistedRecord(ctx, res)
		require.NoError(t, err)
		return rec.ResultID
	}
	var parent dagql.ObjectResult[*core.Directory]
	require.NoError(t, srv.Select(ctx, srv.Root(), &parent, dagql.Selector{Field: "directory"}))
	var sources dagql.ArrayInput[core.FileID]
	var sourceRows []uint64
	// b.txt twice: the later copy wins, as in a withFile chain.
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "b.txt"} {
		var file dagql.ObjectResult[*core.File]
		require.NoError(t, srv.Select(ctx, srv.Root(), &file, dagql.Selector{Field: "savedFile", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString(name)}}}))
		id, err := file.ID()
		require.NoError(t, err)
		sources = append(sources, dagql.NewID[*core.File](id))
		sourceRows = append(sourceRows, rowID(file))
	}

	var dir dagql.ObjectResult[*core.Directory]
	require.NoError(t, srv.Select(ctx, parent, &dir, dagql.Selector{Field: "withFiles", Args: []dagql.NamedInput{
		{Name: "path", Value: dagql.NewString("sub/")},
		{Name: "sources", Value: sources},
		{Name: "permissions", Value: dagql.Opt(dagql.Int(0o600))},
	}}))
	lazy, ok := dir.Self().Lazy.(*core.DirectoryWithFilesLazy)
	require.True(t, ok, "withFiles returned %T, not one copy", dir.Self().Lazy)
	require.False(t, lazy.IsEvaluated())
	require.Equal(t, rowID(parent), rowID(lazy.Parent), "the copy is onto the receiver")
	require.Equal(t, []string{"sub/a.txt", "sub/b.txt", "sub/c.txt", "sub/b.txt"}, lazy.DestPaths)
	require.Len(t, lazy.Sources, len(sourceRows))
	for i, source := range lazy.Sources {
		require.Equal(t, sourceRows[i], rowID(source))
	}
	require.Equal(t, 0o600, *lazy.Permissions)

	rec, err := cache.CapturePersistedRecord(ctx, dir)
	require.NoError(t, err)
	require.Equal(t, "withFiles", rec.Call.Field)
	var payload struct {
		Form, LazyKind string
		LazyJSON       json.RawMessage
	}
	require.NoError(t, json.Unmarshal(rec.Envelope.ObjectJSON, &payload))
	require.Equal(t, "lazy", payload.Form)
	require.Equal(t, "directory.withFiles", payload.LazyKind)
	var recipe struct {
		ParentResultID  uint64   `json:"parentResultID"`
		DestPaths       []string `json:"destPaths"`
		SourceResultIDs []uint64 `json:"sourceResultIDs"`
		Permissions     *int     `json:"permissions"`
	}
	require.NoError(t, json.Unmarshal(payload.LazyJSON, &recipe))
	require.Equal(t, rowID(parent), recipe.ParentResultID)
	require.Equal(t, lazy.DestPaths, recipe.DestPaths)
	require.Equal(t, sourceRows, recipe.SourceResultIDs)
	require.Equal(t, 0o600, *recipe.Permissions)

	decoded, err := (&core.Directory{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, 0, nil), rec.Envelope.ObjectJSON)
	require.NoError(t, err)
	decodedLazy, ok := decoded.(*core.Directory).Lazy.(*core.DirectoryWithFilesLazy)
	require.True(t, ok)
	require.Equal(t, rowID(parent), rowID(decodedLazy.Parent))
	require.Equal(t, lazy.DestPaths, decodedLazy.DestPaths)
	require.Len(t, decodedLazy.Sources, len(sourceRows))
	for i, source := range decodedLazy.Sources {
		require.Equal(t, sourceRows[i], rowID(source))
	}
	require.Equal(t, 0o600, *decodedLazy.Permissions)

	// One source is the same result as withFile, as before.
	var single, withFile dagql.ObjectResult[*core.Directory]
	require.NoError(t, srv.Select(ctx, parent, &single, dagql.Selector{Field: "withFiles", Args: []dagql.NamedInput{
		{Name: "path", Value: dagql.NewString("sub/")},
		{Name: "sources", Value: sources[:1]},
		{Name: "permissions", Value: dagql.Opt(dagql.Int(0o600))},
	}}))
	require.NoError(t, srv.Select(ctx, parent, &withFile, dagql.Selector{Field: "withFile", Args: []dagql.NamedInput{
		{Name: "path", Value: dagql.NewString("sub/a.txt")},
		{Name: "source", Value: sources[0]},
		{Name: "permissions", Value: dagql.Opt(dagql.Int(0o600))},
	}}))
	require.IsType(t, &core.DirectoryWithFileLazy{}, single.Self().Lazy)
	require.Equal(t, rowID(withFile), rowID(single))

	// No sources is the receiver itself, as before.
	var empty dagql.ObjectResult[*core.Directory]
	require.NoError(t, srv.Select(ctx, parent, &empty, dagql.Selector{Field: "withFiles", Args: []dagql.NamedInput{
		{Name: "path", Value: dagql.NewString("sub")},
		{Name: "sources", Value: dagql.ArrayInput[core.FileID]{}},
	}}))
	require.Equal(t, rowID(parent), rowID(empty))
}
