package schema

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
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
	sparseTreeTestFixtureOn(t, e, entries, true)
}

// sparseTreeTestFixtureOn is sparseTreeTestFixture, on an engine that builds
// no sparse tree of its own when buildSparse is false: its sparseTreeTest
// fails if it runs, so its tree can only be another engine's record.
func sparseTreeTestFixtureOn(t *testing.T, e *transferTestEngine, entries []sparseTreeTestEntry, buildSparse bool) {
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
			if !buildSparse {
				return nil, fmt.Errorf("this engine builds no sparse tree of its own")
			}
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
// has asks its sparse tree the same questions as the first, for detection and
// the config readers: does looking a path up in it answer as the full checkout
// does. On a warm engine with no remote cache, the later session gets the
// first session's answers and reads nothing of the tree: the engine reports no
// read of it for that session, as it would to the remote cache, and so neither
// evaluates nor mounts it. A fresh engine served the same answers from another
// engine's records likewise never unpacks the tree to learn what it holds.
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

// A commit's path can be any bytes, not only UTF-8. A read that selects a file
// the sparse tree doesn't hold is written from the files ReadFiles lists, so
// the list must name each file with its own bytes: a path that came back
// changed would name a file the commit doesn't have.
func TestGitSparseReadFilesKeepsAPathsBytes(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	oid := strings.Repeat("7", 40)
	sparseTreeTestFixture(t, e, []sparseTreeTestEntry{
		{'f', "100644", strings.Repeat("1", 40), "dagger.toml", "[modules]\n"},
		{'a', "100644", oid, "outside/caf\xe9.txt", "Latin-1, as some commits name their files"},
	})
	ctx := e.session(t, "reader")
	root, _ := sparseTreeTestTrees(t, e, ctx)
	files, err := (&core.GitSparseTree{Root: root}).ReadFiles(ctx, "outside", core.CopyFilter{Include: []string{"*.txt"}})
	require.NoError(t, err)
	require.Equal(t, []string{"100644 " + oid + " outside/caf\xe9.txt"}, files.Files)
}

// An engine served another engine's records gets that engine's answers to
// detection's lookups in a Git workspace's sparse tree and never evaluates the
// tree or reads any of its content, as a fresh engine on a same-commit rerun is
// served them.
//
// An answer can then send a read to the sparse tree on an engine that can't
// get the tree's files, where reading the list from them would have failed and
// sent the read to the full tree. Such a read reads the full tree, and never
// fails: a lookup's own read the cache has no answer for, and a read of the
// workspace's files, whose route comes from the list itself.
func TestGitSparseTreeOnAnEngineServedAnothersAnswers(t *testing.T) {
	t.Parallel()
	salt := transferTestSalt(t)
	entries := []sparseTreeTestEntry{
		{'f', "100644", strings.Repeat("1", 40), "dagger.toml", "[modules.mod]\nsource = \"mod\"\n"},
		{'f', "100644", strings.Repeat("2", 40), "mod/dagger.json", `{"name": "mod"}`},
		{'a', "100644", strings.Repeat("4", 40), "mod/big.bin", "only in the full tree"},
		{'a', "100644", strings.Repeat("5", 40), "outside/z.txt", "only in the full tree"},
		{'a', "100644", strings.Repeat("6", 40), "outside/y.txt", "only in the full tree"},
	}
	a := newTransferTestEngine(t, salt)
	sparseTreeTestFixture(t, a, entries)
	a1 := a.session(t, "a1")
	rootA, _ := sparseTreeTestTrees(t, a, a1)
	require.NoError(t, a.cache.Evaluate(a1, rootA))

	// What A's session asked, as detection asks: the lookups of dagger.toml
	// and big.bin, and dagger.toml's existence. It never read dagger.toml's
	// contents.
	sel := func(field string, args ...dagql.NamedInput) dagql.AnyResult {
		t.Helper()
		var res dagql.AnyResult
		require.NoError(t, a.dag.Select(a1, rootA, &res, dagql.Selector{Field: field, Args: args}))
		return res
	}
	path := func(p string) dagql.NamedInput { return dagql.NamedInput{Name: "path", Value: dagql.NewString(p)} }
	roots := []dagql.AnyResult{
		sel("__gitSparseCovers", path("dagger.toml")),
		sel("__gitSparseCovers", path("mod/big.bin")),
		sel("exists", path("dagger.toml")),
	}

	b := newTransferTestEngine(t, salt)
	sparseTreeTestFixtureOn(t, b, entries, false)
	selection := dagql.ValueSelection{
		Roots:   roots,
		Outputs: []dagql.SelectedValueOutput{{Result: rootA, Address: dagql.PersistedPartAddress{Part: "snapshot"}}},
	}
	require.NoError(t, a.cache.WithExportedValues(a1, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		require.Len(t, exported.Chains.Entries, 1, "the sparse tree's chain is offered")
		var unavailable atomic.Bool
		provider := &testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider, BeforeRead: func(context.Context, ocispec.Descriptor) error {
			if unavailable.Load() {
				return errors.New("the sparse tree's content can't be had")
			}
			return nil
		}}
		b.cache.SetPartContentSource(fileContentsTestSource{provider})
		b1 := b.session(t, "b1")
		_, err := b.cache.MergeValues(b1, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)

		root, full := sparseTreeTestTrees(t, b, b1)
		sparse := &core.GitSparseTree{Root: root}
		fs := &core.GitSparseTreeFS{Sparse: sparse, Full: full}
		_, exists, err := fs.Exists(b1, "dagger.toml")
		require.NoError(t, err)
		require.True(t, exists)
		_, exists, err = fs.Exists(b1, "mod/big.bin")
		require.NoError(t, err)
		require.True(t, exists, "a file only the full tree has is looked up there")
		require.Zero(t, provider.Reads.Load(), "no chain of the sparse tree's is read")
		require.True(t, dagql.HasPendingLazyComputation(root), "the sparse tree is never evaluated")

		// Now the tree's content can't be had. The cache's answer sends the
		// read of dagger.toml's contents to the sparse tree, and it has no
		// answer for that read itself: the full tree serves it.
		unavailable.Store(true)
		data, err := fs.ReadFile(b1, "dagger.toml")
		require.NoError(t, err)
		require.Equal(t, "[modules.mod]\nsource = \"mod\"\n", string(data))
		// And a read of the workspace's files, whose route needs the list:
		// one a tree of its own would serve, and one this tree would.
		got, err := gitSparseReadRoot(b1, b.dag, sparse, dagql.Result[*core.GitRef]{}, full, "", core.CopyFilter{Include: []string{"outside/y.txt"}}, false)
		require.NoError(t, err)
		gotID, ok := dagql.CacheResultNumber(got)
		require.True(t, ok)
		fullID, ok := dagql.CacheResultNumber(full)
		require.True(t, ok)
		require.Equal(t, fullID, gotID, "the read comes from the full tree")
		got, err = gitSparseReadRoot(b1, b.dag, sparse, dagql.Result[*core.GitRef]{}, full, "mod", core.CopyFilter{Include: []string{"dagger.json"}}, false)
		require.NoError(t, err)
		gotID, ok = dagql.CacheResultNumber(got)
		require.True(t, ok)
		require.Equal(t, fullID, gotID, "the planned read comes from the full tree")
		return nil
	}))
}

// A read of a Git workspace's files that runs asks its sparse tree where it
// comes from and which files it selects, and copies out of that tree, or a
// tree of its own, next. It works both out from the tree's list, held once a
// session, with no call per read: the Go SDK's generator makes about 3,700
// distinct reads in one check on a whole pull request's fill, and as calls
// those were 3,865 entries, records and lookups on its engine, about 3 ms each,
// on every fill and edit. A lookup of a path, which detection asks under no
// cached call, stays a call.
func TestGitSparseReadsAreNotCalls(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	sparseTreeTestFixture(t, e, []sparseTreeTestEntry{
		{'f', "100644", strings.Repeat("1", 40), "dagger.toml", "[modules.mod]\nsource = \"mod\"\n"},
		{'f', "100644", strings.Repeat("2", 40), "mod/dagger.json", `{"name": "mod"}`},
		{'f', "100644", strings.Repeat("3", 40), "mod/main.go", "package main\n"},
		{'a', "100644", strings.Repeat("4", 40), "mod/big.bin", "only in the full tree"},
		{'a', "100644", strings.Repeat("5", 40), "outside/z.txt", "only in the full tree"},
	})
	ctx := e.session(t, "reader")
	root, full := sparseTreeTestTrees(t, e, ctx)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(context.WithoutCancel(ctx))
	ctx, span := provider.Tracer("sparse-reads").Start(ctx, "reads")
	sparse := &core.GitSparseTree{Root: root}
	for i := range 20 {
		source, err := sparse.ReadSource(ctx, "mod", core.CopyFilter{Include: []string{"dagger.json", fmt.Sprintf("main%d.go", i)}})
		require.NoError(t, err)
		require.Equal(t, core.GitSparseReadPlanned, source, "a read of what the sparse tree holds")
		source, err = sparse.ReadSource(ctx, "", core.CopyFilter{Include: []string{"outside/z.txt", fmt.Sprintf("outside/%d.txt", i)}})
		require.NoError(t, err)
		require.Equal(t, core.GitSparseReadOwn, source, "a read of a file the sparse tree doesn't hold")
		files, err := sparse.ReadFiles(ctx, "", core.CopyFilter{Include: []string{"outside/z.txt", fmt.Sprintf("outside/%d.txt", i)}})
		require.NoError(t, err)
		require.Equal(t, &core.GitSparseReadFiles{
			Files: []string{"100644 " + strings.Repeat("5", 40) + " outside/z.txt"},
			Dirs:  []string{"outside"},
		}, files)
	}
	// A lookup is a call, which the recorder sees.
	_, exists, err := (&core.GitSparseTreeFS{Sparse: sparse, Full: full}).Exists(ctx, "dagger.toml")
	require.NoError(t, err)
	require.True(t, exists)
	span.End()

	calls := map[string]int{}
	for _, ended := range recorder.Ended() {
		calls[ended.Name()]++
	}
	require.Positive(t, calls["Directory.__gitSparseCovers"], "the recorder sees the engine's calls: %v", calls)
	for name := range calls {
		require.False(t, strings.HasPrefix(name, "Directory.__gitSparseRead"), "a read made a call, %s: %v", name, calls)
	}
}

// sparseTreeTestWorkspace is the Git workspace over the fixture's trees as a
// session's current workspace is (Query.currentWorkspace): a recipe of its own
// in each session, identified by its content, and attached to that session.
// edit, if set, changes it first.
func sparseTreeTestWorkspace(t *testing.T, e *transferTestEngine, ctx context.Context, session string, edit func(*core.Workspace) *core.Workspace) dagql.ObjectResult[*core.Workspace] {
	t.Helper()
	sparse, full := sparseTreeTestTrees(t, e, ctx)
	ws := &core.Workspace{}
	ws.SetRootfs(full)
	ws.SetSource(&core.WorkspaceSourceGitRef{Sparse: &core.GitSparseTree{Root: sparse}})
	if edit != nil {
		ws = edit(ws)
	}
	res := objectResult(t, e.dag, "sparse-tree-workspace-"+session, ws)
	identity, ok, err := ws.ContentIdentity(ctx)
	require.NoError(t, err)
	require.True(t, ok, "a Git workspace is identified by its content")
	res, err = res.WithContentDigest(ctx, identity)
	require.NoError(t, err)
	attached, err := e.cache.AttachResult(ctx, session+"-session", e.dag, res)
	require.NoError(t, err)
	return attached.(dagql.ObjectResult[*core.Workspace])
}

// sparseTreeTestModuleFS is the filesystem module loading asks of the module
// at mod/ in ws, as a module source loaded through the workspace has it.
func sparseTreeTestModuleFS(ws dagql.ObjectResult[*core.Workspace]) *core.ModuleSourceFS {
	return core.NewModuleSourceFS(nil, &core.ModuleSource{
		Kind:              core.ModuleSourceKindGit,
		SourceRootSubpath: "mod",
		Workspace:         ws,
	})
}

// The probe module loading makes of a path in its workspace: a dependency ref
// that looks remote is first tried as a local path, which a pinned Git
// workspace doesn't have, and a module's config is looked for by each of its
// file names.
const sparseTreeTestAbsentDep = "github.com/dagger/go@79a92beb4df0898400ef7f7a632009035290383b"

// Module loading asks whether paths exist in a pinned Git workspace: a
// dependency whose ref looks remote is first tried as a local path, and a
// module's config is looked for. A later session on the engine gets the first
// session's answers, for a path the workspace doesn't have as for one it has,
// from the cache: it runs nothing to get them, and so reads nothing of the
// workspace's sparse tree, which on a fresh engine would unpack the tree on the
// session's path. Each session's workspace is its own recipe, as
// Query.currentWorkspace makes it.
func TestModuleLoadingAsksAPinnedWorkspaceNothingInALaterSession(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	sparseTreeTestFixture(t, e, []sparseTreeTestEntry{
		{'f', "100644", strings.Repeat("1", 40), "dagger.toml", "[modules.mod]\nsource = \"mod\"\n"},
		{'f', "100644", strings.Repeat("2", 40), "mod/dagger.json", `{"name": "mod"}`},
		{'f', "100644", strings.Repeat("3", 40), "mod/main.go", "package main\n"},
		{'a', "100644", strings.Repeat("4", 40), "mod/big.bin", "only in the full tree"},
	})
	// A session that holds both trees throughout, as the engine's cache holds
	// a commit's trees between its sessions.
	holder := e.session(t, "holder")
	sparseTreeTestTrees(t, e, holder)

	probe := func(ctx context.Context, session string) {
		t.Helper()
		fs := sparseTreeTestModuleFS(sparseTreeTestWorkspace(t, e, ctx, session, nil))
		_, exists, err := fs.Exists(ctx, sparseTreeTestAbsentDep)
		require.NoError(t, err)
		require.False(t, exists, "the workspace has no such path")
		_, exists, err = fs.Exists(ctx, "dagger.json")
		require.NoError(t, err)
		require.True(t, exists, "the module's config")
		_, exists, err = fs.Exists(ctx, "dagger-module.toml")
		require.NoError(t, err)
		require.False(t, exists, "a config file name the module doesn't use")
	}

	first := e.session(t, "first")
	probe(first, "first")
	sparseTreeTestEndSession(t, e, "first")

	second := e.session(t, "second")
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(context.WithoutCancel(second))
	traced, span := provider.Tracer("module-loading").Start(second, "probe")
	probe(traced, "second")
	span.End()

	outcomes := map[string][]string{}
	for _, ended := range recorder.Ended() {
		for _, attr := range ended.Attributes() {
			if string(attr.Key) == "dagger.io/cache.outcome" {
				outcome := strings.Trim(attr.Value.String(), `"`)
				outcomes[outcome] = append(outcomes[outcome], ended.Name())
			}
		}
	}
	require.NotEmpty(t, outcomes["hit"], "the recorder sees the later session's calls: %v", outcomes)
	require.Empty(t, outcomes["executed"], "the later session runs nothing to learn whether paths exist: %v", outcomes)
}

// An engine served another engine's records loads a module from the same
// pinned Git workspace, as a fresh engine on a same-commit rerun does: it gets
// that engine's answers to whether paths exist, for a path the workspace
// doesn't have as for one it has, and never evaluates the workspace's sparse
// tree or reads any of its content.
func TestModuleLoadingOnAnEngineServedAnothersAnswers(t *testing.T) {
	t.Parallel()
	salt := transferTestSalt(t)
	entries := []sparseTreeTestEntry{
		{'f', "100644", strings.Repeat("1", 40), "dagger.toml", "[modules.mod]\nsource = \"mod\"\n"},
		{'f', "100644", strings.Repeat("2", 40), "mod/dagger.json", `{"name": "mod"}`},
		{'a', "100644", strings.Repeat("4", 40), "mod/big.bin", "only in the full tree"},
	}
	probe := func(t *testing.T, fs *core.ModuleSourceFS, ctx context.Context) {
		t.Helper()
		_, exists, err := fs.Exists(ctx, sparseTreeTestAbsentDep)
		require.NoError(t, err)
		require.False(t, exists, "the workspace has no such path")
		_, exists, err = fs.Exists(ctx, "dagger.json")
		require.NoError(t, err)
		require.True(t, exists, "the module's config")
	}

	a := newTransferTestEngine(t, salt)
	sparseTreeTestFixture(t, a, entries)
	a1 := a.session(t, "a1")
	rootA, _ := sparseTreeTestTrees(t, a, a1)
	require.NoError(t, a.cache.Evaluate(a1, rootA))
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(context.WithoutCancel(a1))
	traced, span := provider.Tracer("module-loading").Start(a1, "probe")
	probe(t, sparseTreeTestModuleFS(sparseTreeTestWorkspace(t, a, a1, "a1", nil)), traced)
	span.End()

	// What A's session kept, as a set carries a check's calls to the next
	// engine: every result its probe made or hit.
	var roots []dagql.AnyResult
	var releases []func(context.Context) error
	defer func() {
		for _, release := range releases {
			require.NoError(t, release(context.WithoutCancel(a1)))
		}
	}()
	seen := map[uint64]bool{}
	for _, ended := range recorder.Ended() {
		for _, attr := range ended.Attributes() {
			if string(attr.Key) != "dagger.io/cache.result.id" {
				continue
			}
			var id uint64
			_, err := fmt.Sscan(strings.Trim(attr.Value.String(), `"`), &id)
			require.NoError(t, err)
			if seen[id] {
				continue
			}
			seen[id] = true
			res, release, err := a.cache.HoldEntry(a1, id)
			if err != nil {
				// An entry with no value of its own, which no set carries.
				continue
			}
			releases = append(releases, release)
			roots = append(roots, res)
		}
	}
	require.NotEmpty(t, roots, "A's probe made results")

	b := newTransferTestEngine(t, salt)
	sparseTreeTestFixtureOn(t, b, entries, false)
	selection := dagql.ValueSelection{
		Roots:   roots,
		Outputs: []dagql.SelectedValueOutput{{Result: rootA, Address: dagql.PersistedPartAddress{Part: "snapshot"}}},
	}
	require.NoError(t, a.cache.WithExportedValues(a1, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		require.Len(t, exported.Chains.Entries, 1, "the sparse tree's chain is offered")
		provider := &testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider}
		b.cache.SetPartContentSource(fileContentsTestSource{provider})
		b1 := b.session(t, "b1")
		_, err := b.cache.MergeValues(b1, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)

		probe(t, sparseTreeTestModuleFS(sparseTreeTestWorkspace(t, b, b1, "b1", nil)), b1)
		root, _ := sparseTreeTestTrees(t, b, b1)
		require.Zero(t, provider.Reads.Load(), "no chain of the sparse tree's is read")
		require.True(t, dagql.HasPendingLazyComputation(root), "the sparse tree is never evaluated")
		return nil
	}))
}

// Where the commit's tree alone doesn't answer for a path, module loading asks
// as it did: a path only the full checkout has is looked up there, as
// detection looks it up; a path under something mounted on the workspace is
// the mount's; and a workspace that is a client's host directory is read
// through the workspace, per client, never asked of a tree.
func TestModuleLoadingAsksWhatAnswersForAPath(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	sparseTreeTestFixture(t, e, []sparseTreeTestEntry{
		{'f', "100644", strings.Repeat("1", 40), "dagger.toml", "[modules.mod]\nsource = \"mod\"\n"},
		{'f', "100644", strings.Repeat("2", 40), "mod/dagger.json", `{"name": "mod"}`},
		{'a', "100644", strings.Repeat("4", 40), "mod/big.bin", "only in the full tree"},
	})

	t.Run("a path only the full tree has", func(t *testing.T) {
		ctx := e.session(t, "uncovered")
		fs := sparseTreeTestModuleFS(sparseTreeTestWorkspace(t, e, ctx, "uncovered", nil))
		_, exists, err := fs.Exists(ctx, "big.bin")
		require.NoError(t, err)
		require.True(t, exists, "the full tree has it")
	})

	t.Run("a commit with a submodule", func(t *testing.T) {
		// The sparse tree answers for no path of a commit with a submodule,
		// whose files its list can't name: the full tree answers for all.
		sub := newTransferTestEngine(t, transferTestSalt(t))
		sparseTreeTestFixture(t, sub, []sparseTreeTestEntry{
			{'g', "160000", strings.Repeat("8", 40), "mod/vendor", ""},
		})
		ctx := sub.session(t, "submodule")
		fs := sparseTreeTestModuleFS(sparseTreeTestWorkspace(t, sub, ctx, "submodule", nil))
		_, exists, err := fs.Exists(ctx, "vendor")
		require.NoError(t, err)
		require.True(t, exists, "the full tree has the submodule")
		_, exists, err = fs.Exists(ctx, sparseTreeTestAbsentDep)
		require.NoError(t, err)
		require.False(t, exists, "nor does the full tree have this")
	})

	t.Run("a path under a mount", func(t *testing.T) {
		ctx := e.session(t, "mounted")
		var mounts dagql.ObjectResult[*core.Directory]
		require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &mounts,
			dagql.Selector{Field: "directory"},
			dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("mod/vendored/dagger.json")},
				{Name: "contents", Value: dagql.NewString(`{"name": "vendored"}`)},
			}}))
		fs := sparseTreeTestModuleFS(sparseTreeTestWorkspace(t, e, ctx, "mounted", func(ws *core.Workspace) *core.Workspace {
			return ws.WithMounted(mounts, "mod/vendored")
		}))
		_, exists, err := fs.Exists(ctx, "vendored/dagger.json")
		require.NoError(t, err)
		require.True(t, exists, "the mount has it, though the commit doesn't")
	})

	t.Run("a client's host directory", func(t *testing.T) {
		ctx := e.session(t, "host")
		recorder := tracetest.NewSpanRecorder()
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		defer provider.Shutdown(context.WithoutCancel(ctx))
		traced, span := provider.Tracer("module-loading").Start(ctx, "probe")
		ws := &core.Workspace{}
		ws.SetHostPath("/work")
		ws.SetSource(core.NewWorkspaceSourceClientLocal("/work"))
		res := objectResult(t, e.dag, "host-workspace", ws)
		attached, err := e.cache.AttachResult(ctx, "host-session", e.dag, res)
		require.NoError(t, err)
		// This engine has no client host to read, so the read fails: what
		// matters is what the probe asked.
		_, _, _ = sparseTreeTestModuleFS(attached.(dagql.ObjectResult[*core.Workspace])).Exists(traced, sparseTreeTestAbsentDep)
		span.End()
		calls := map[string]int{}
		for _, ended := range recorder.Ended() {
			calls[ended.Name()]++
		}
		require.Positive(t, calls["Workspace.directory"], "the probe reads through the workspace: %v", calls)
		require.Zero(t, calls["Directory.__gitSparseCovers"], "and asks no tree: %v", calls)
	})
}
