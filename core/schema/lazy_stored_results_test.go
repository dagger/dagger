package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

func TestLazyStoredResultsWithoutBacking(t *testing.T) {
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	actx, a, asrv := scratchTestCache(t, aStore, "", "stored-a")
	bctx, b, bsrv := scratchTestCache(t, bStore, "", "stored-b")
	for _, srv := range []*dagql.Server{asrv, bsrv} {
		srv.InstallObject(dagql.NewClass[*core.File](srv))
		srv.InstallObject(dagql.NewClass[*core.Container](srv))
		srv.InstallObject(dagql.NewClass[*core.SearchSubmatch](srv))
		srv.InstallObject(dagql.NewClass[*core.SearchResult](srv))
		fs := &fileSchema{}
		dagql.Fields[*core.File]{dagql.NodeFunc("contents", fs.contents), dagql.NodeFunc("search", fs.search)}.Install(srv)
		cs := &containerSchema{}
		dagql.Fields[*core.Container]{dagql.NodeFunc("stdout", cs.stdout), dagql.NodeFunc("stderr", cs.stderr)}.Install(srv)
	}
	ref, _ := aStore.Build(t, nil, "data", "saved text\n")
	file := &core.File{Platform: core.Platform{OS: "linux", Architecture: "arm64"}, File: new(core.LazyAccessor[string, *core.File]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File])}
	file.SetPath("/data")
	file.SetSnapshot(ref)
	ctr := core.NewContainer(file.Platform)
	meta, _ := aStore.Build(t, nil, "stdout", "saved stdout\n")
	ctr.MetaSnapshot.SetValue(meta)
	dagql.Fields[*core.Query]{dagql.Func("savedFile", func(context.Context, *core.Query, struct{}) (*core.File, error) { return file, nil }), dagql.Func("savedContainer", func(context.Context, *core.Query, struct{}) (*core.Container, error) { return ctr, nil })}.Install(asrv)
	var f dagql.ObjectResult[*core.File]
	var c dagql.ObjectResult[*core.Container]
	require.NoError(t, asrv.Select(actx, asrv.Root(), &f, dagql.Selector{Field: "savedFile"}))
	require.NoError(t, asrv.Select(actx, asrv.Root(), &c, dagql.Selector{Field: "savedContainer"}))
	contents, err := f.Select(actx, asrv, dagql.Selector{Field: "contents"})
	require.NoError(t, err)
	searchCall := dagql.Selector{Field: "search", Args: []dagql.NamedInput{{Name: "pattern", Value: dagql.String("saved")}}}
	search, err := f.Select(actx, asrv, searchCall)
	require.NoError(t, err)
	stdout, err := c.Select(actx, asrv, dagql.Selector{Field: "stdout"})
	require.NoError(t, err)
	roots := []dagql.AnyResult{contents, search, stdout}
	require.NoError(t, a.WithExportedValues(actx, dagql.ValueSelection{Roots: roots}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		require.Empty(t, exported.Chains.Entries, "this proof transfers only stored values")
		imported, err := b.ImportValues(bctx, exported.Bundle)
		require.NoError(t, err)
		require.Len(t, imported, 3)
		var fileParent dagql.ObjectResult[*core.File]
		var containerParent dagql.ObjectResult[*core.Container]
		for i, row := range imported {
			value, err := b.LoadResultByResultID(bctx, "stored-b", bsrv, row.ResultID)
			require.NoError(t, err)
			require.NoError(t, b.Evaluate(bctx, value))
			frame, err := value.ResultCall()
			require.NoError(t, err)
			require.NotNil(t, frame.Receiver)
			parent, err := b.LoadResultByResultID(bctx, "stored-b", bsrv, frame.Receiver.ResultID)
			require.NoError(t, err)
			if i < 2 {
				fileParent = parent.(dagql.ObjectResult[*core.File])
			} else {
				containerParent = parent.(dagql.ObjectResult[*core.Container])
			}
		}
		gotContents, err := fileParent.Select(bctx, bsrv, dagql.Selector{Field: "contents"})
		require.NoError(t, err)
		gotSearch, err := fileParent.Select(bctx, bsrv, searchCall)
		require.NoError(t, err)
		gotStdout, err := containerParent.Select(bctx, bsrv, dagql.Selector{Field: "stdout"})
		require.NoError(t, err)
		require.Equal(t, contents.Unwrap(), gotContents.Unwrap())
		require.Equal(t, stdout.Unwrap(), gotStdout.Unwrap())
		wantList, gotList := search.Unwrap().(dagql.Enumerable), gotSearch.Unwrap().(dagql.Enumerable)
		require.Equal(t, 1, wantList.Len())
		require.Equal(t, 1, gotList.Len())
		wantItem, err := wantList.Nth(1)
		require.NoError(t, err)
		gotItem, err := gotList.Nth(1)
		require.NoError(t, err)
		wantSearch, ok := dagql.UnwrapAs[*core.SearchResult](wantItem)
		require.True(t, ok)
		gotSearchItem, ok := dagql.UnwrapAs[*core.SearchResult](gotItem)
		require.True(t, ok)
		require.Equal(t, wantSearch, gotSearchItem)
		_, fileSet := fileParent.Self().Snapshot.Peek()
		require.False(t, fileSet)
		_, metaSet := containerParent.Self().MetaSnapshot.Peek()
		require.False(t, metaSet)
		// These distinct calls are absent and therefore must demand backing.
		_, err = fileParent.Select(bctx, bsrv, dagql.Selector{Field: "contents", Args: []dagql.NamedInput{{Name: "offsetLines", Value: dagql.Opt(dagql.Int(0))}}})
		require.ErrorIs(t, err, dagql.ErrUnavailablePart)
		searchCall.Args[0].Value = dagql.String("absent")
		_, err = fileParent.Select(bctx, bsrv, searchCall)
		require.ErrorIs(t, err, dagql.ErrUnavailablePart)
		_, err = containerParent.Select(bctx, bsrv, dagql.Selector{Field: "stderr"})
		require.ErrorIs(t, err, dagql.ErrUnavailablePart)
		report, err := b.TransferFixtureSnapshot(bctx, "stored-b", nil)
		require.NoError(t, err)
		for _, event := range report.Parts {
			require.NotEqual(t, "lazy-enter", event.Kind)
			require.NotEqual(t, "provider-read", event.Kind)
		}
		t.Log("stored contents/search/stdout hits returned saved values with no backing; absent calls demanded missing parts and failed")
		return nil
	}))
}
