package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// sparseTreeTestEntry is one path of a commit's tree as a sparse workspace
// tree lists it: its mark in the manifest ('f' when the sparse tree holds it,
// 'a' when only the full tree does), its mode and its object ID.
type sparseTreeTestEntry struct {
	mark       byte
	mode, oid  string
	path, body string
}

// sparseTreeTestFixture is a commit's tree two ways, as a Git workspace has
// it: its sparse workspace tree, its files under tree/ beside the manifest and
// objects lists, as __sparseWorkspaceTree writes them, and its full checkout.
// Each is a persistable call of the engine's, so a later session's selection
// of either is the same result.
func sparseTreeTestFixture(t *testing.T, e *transferTestEngine, entries []sparseTreeTestEntry) {
	t.Helper()
	var manifest, objects strings.Builder
	for _, entry := range entries {
		manifest.WriteString(string(entry.mark) + entry.path + "\x00")
		objects.WriteString(entry.mode + " " + entry.oid + " " + entry.path + "\x00")
	}
	write := func(root, p, body string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, p), []byte(body), 0o644))
	}
	sparseRef, _ := e.store.BuildWith(t, nil, func(root string) {
		write(root, "manifest", manifest.String())
		write(root, "objects", objects.String())
		for _, entry := range entries {
			if entry.mark == 'f' {
				write(root, filepath.Join("tree", entry.path), entry.body)
			}
		}
	})
	fullRef, _ := e.store.BuildWith(t, nil, func(root string) {
		for _, entry := range entries {
			write(root, entry.path, entry.body)
		}
	})
	directory := func(ref bkcache.ImmutableRef, dir string) *core.Directory {
		d := &core.Directory{
			Platform: core.Platform{OS: "linux", Architecture: "amd64"},
			Dir:      new(core.LazyAccessor[string, *core.Directory]),
			Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory]),
		}
		d.SetPath(dir)
		d.SetSnapshot(ref)
		return d
	}
	dagql.Fields[*core.Query]{
		dagql.Func("sparseTreeTest", func(context.Context, *core.Query, struct{}) (*core.Directory, error) {
			return directory(sparseRef, "/tree"), nil
		}).IsPersistable(),
		dagql.Func("fullTreeTest", func(context.Context, *core.Query, struct{}) (*core.Directory, error) {
			return directory(fullRef, "/"), nil
		}).IsPersistable(),
	}.Install(e.dag)
}

// sparseTreeTestTrees selects the fixture's two trees in ctx's session.
func sparseTreeTestTrees(t *testing.T, e *transferTestEngine, ctx context.Context) (sparse, full dagql.ObjectResult[*core.Directory]) {
	t.Helper()
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &sparse, dagql.Selector{Field: "sparseTreeTest"}))
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &full, dagql.Selector{Field: "fullTreeTest"}))
	return sparse, full
}

// sparseTreeTestEndSession ends a session, as a check's does.
func sparseTreeTestEndSession(t *testing.T, e *transferTestEngine, name string) {
	t.Helper()
	require.NoError(t, e.cache.ReleaseSession(e.base, name+"-session"))
	require.NoError(t, e.cache.WaitSessionRelease(e.base, name+"-session"))
}

// A later session that loads a Git workspace at a commit the engine already
// has asks its sparse tree the same questions as the first: does looking a
// path up in it answer as the full checkout does, where does a filtered read
// come from, and which files does it select. On a warm engine with no remote
// cache, the later session gets the first session's answers and reads nothing
// of the tree: the engine reports no read of it for that session, as it would
// to the remote cache, and so neither evaluates nor mounts it. A fresh engine
// served the same answers from another engine's records likewise never
// unpacks the tree to learn what it holds.
func TestGitSparseTreeQuestionsReadNothingOfTheTreeInALaterSession(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var reads []dagql.PartRead
	e := newTransferTestEngineWith(t, transferTestSalt(t), dagql.WithPartReadReport(func(batch []dagql.PartRead) {
		mu.Lock()
		defer mu.Unlock()
		reads = append(reads, batch...)
	}))
	sparseTreeTestFixture(t, e, []sparseTreeTestEntry{
		{'f', "100644", "1111111111111111111111111111111111111111", "dagger.toml", "[modules.mod]\nsource = \"mod\"\n"},
		{'f', "100644", "2222222222222222222222222222222222222222", "mod/dagger.json", `{"name": "mod"}`},
		{'f', "100644", "3333333333333333333333333333333333333333", "mod/main.go", "package main\n"},
		{'a', "100644", "4444444444444444444444444444444444444444", "mod/big.bin", "only in the full tree"},
		{'a', "100644", "5555555555555555555555555555555555555555", "outside/z.txt", "only in the full tree"},
	})

	// A session that holds both trees throughout, as the engine's cache holds
	// a commit's trees between its sessions.
	holder := e.session(t, "holder")
	sparseTreeTestTrees(t, e, holder)

	ask := func(ctx context.Context) dagql.ObjectResult[*core.Directory] {
		t.Helper()
		root, full := sparseTreeTestTrees(t, e, ctx)
		sparse := &core.GitSparseTree{Root: root}
		fs := &core.GitSparseTreeFS{Sparse: sparse, Full: full}

		// The sparse tree holds dagger.toml, so it answers for it; it doesn't
		// hold big.bin, which only the full tree then finds.
		_, exists, err := fs.Exists(ctx, "dagger.toml")
		require.NoError(t, err)
		require.True(t, exists)
		_, exists, err = fs.Exists(ctx, "mod/big.bin")
		require.NoError(t, err)
		require.True(t, exists, "a file only the full tree has is looked up there")

		source, err := sparse.ReadSource(ctx, "mod", core.CopyFilter{Include: []string{"dagger.json", "main.go"}})
		require.NoError(t, err)
		require.Equal(t, core.GitSparseReadPlanned, source, "a read of what the sparse tree holds")
		source, err = sparse.ReadSource(ctx, "", core.CopyFilter{Include: []string{"outside/z.txt"}})
		require.NoError(t, err)
		require.Equal(t, core.GitSparseReadOwn, source, "a read of a file the sparse tree doesn't hold")
		files, err := sparse.ReadFiles(ctx, "", core.CopyFilter{Include: []string{"outside/z.txt"}})
		require.NoError(t, err)
		require.Equal(t, &core.GitSparseReadFiles{
			Files: []string{"100644 5555555555555555555555555555555555555555 outside/z.txt"},
			Dirs:  []string{"outside"},
		}, files)
		return root
	}

	first := e.session(t, "first")
	root := ask(first)
	sparseTreeTestEndSession(t, e, "first")

	second := e.session(t, "second")
	rootID, ok := dagql.CacheResultNumber(root)
	require.True(t, ok)
	secondRootID, ok := dagql.CacheResultNumber(ask(second))
	require.True(t, ok)
	require.Equal(t, rootID, secondRootID, "the later session's tree is the engine's")
	// A read the later session makes of the full tree at its top, which the
	// engine reports: the report would show a read of the sparse tree too.
	_, full := sparseTreeTestTrees(t, e, second)
	require.NoError(t, e.cache.Evaluate(second, full))
	// Closing the cache reports every read not yet reported.
	require.NoError(t, e.cache.Close(e.base))

	fullID, ok := dagql.CacheResultNumber(full)
	require.True(t, ok)
	mu.Lock()
	defer mu.Unlock()
	var secondReads []uint64
	for _, read := range reads {
		if read.Session == "second-session" {
			secondReads = append(secondReads, read.ResultID)
		}
	}
	require.Contains(t, secondReads, fullID, "the later session's read of the full tree is reported")
	require.NotContains(t, secondReads, rootID, "the later session reads nothing of the sparse tree to learn what it holds")
}

// A read's list of files is as long as what it selects, and a broad read of a
// large repository's sparse tree selects more than the 1 MiB File.contents
// persists. Such an answer isn't kept past the sessions that use it, as a
// large File.contents isn't, so no record holds that many bytes: a later
// session reads the tree's lists again and gets the same answer, as every
// session did before the questions were calls. A short answer is kept.
func TestGitSparseReadFilesOverItsBoundIsAnsweredAgainInALaterSession(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	entries := []sparseTreeTestEntry{
		{'f', "100644", strings.Repeat("1", 40), "dagger.toml", "[modules]\n"},
		{'a', "100644", strings.Repeat("2", 40), "outside/z.txt", "only in the full tree"},
	}
	// 16,000 files of about 70 bytes each in the answer: over 1 MiB.
	for i := range 16000 {
		entries = append(entries, sparseTreeTestEntry{'a', "100644", fmt.Sprintf("%040x", i), fmt.Sprintf("big/file%05d.txt", i), ""})
	}
	sparseTreeTestFixture(t, e, entries)
	holder := e.session(t, "holder")
	sparseTreeTestTrees(t, e, holder)

	readFiles := func(ctx context.Context, dir, include string) dagql.AnyResult {
		t.Helper()
		root, _ := sparseTreeTestTrees(t, e, ctx)
		var res dagql.AnyResult
		require.NoError(t, e.dag.Select(ctx, root, &res, dagql.Selector{Field: "__gitSparseReadFiles", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(dir)},
			{Name: "include", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(include))},
		}}))
		return res
	}
	answer := func(res dagql.AnyResult) *core.GitSparseReadFiles {
		t.Helper()
		raw, ok := res.Unwrap().(core.JSON)
		require.True(t, ok, "the answer is JSON, got %T", res.Unwrap())
		var files *core.GitSparseReadFiles
		require.NoError(t, json.Unmarshal(raw, &files))
		return files
	}

	first := e.session(t, "first")
	small := readFiles(first, "", "outside/z.txt")
	require.False(t, small.HitCache())
	large := readFiles(first, "big", "*.txt")
	require.False(t, large.HitCache())
	require.Greater(t, len(large.Unwrap().(core.JSON)), fileContentsPersistLimit, "the fixture's answer is over the bound")
	want := answer(large)
	require.Len(t, want.Files, 16000)
	sparseTreeTestEndSession(t, e, "first")

	second := e.session(t, "second")
	require.True(t, readFiles(second, "", "outside/z.txt").HitCache(), "a short answer is kept after its session ends")
	again := readFiles(second, "big", "*.txt")
	require.False(t, again.HitCache(), "an answer over the bound isn't kept after its session ends")
	require.Equal(t, want, answer(again), "and is answered again, the same")

	// Through the workspace's own question, too: answered, not failed.
	root, _ := sparseTreeTestTrees(t, e, second)
	files, err := (&core.GitSparseTree{Root: root}).ReadFiles(second, "big", core.CopyFilter{Include: []string{"*.txt"}})
	require.NoError(t, err)
	require.Equal(t, want, files)
}
