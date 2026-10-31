package core

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

func TestModuleTreeReaderWithoutTree(t *testing.T) {
	ctx := t.Context()
	for _, src := range []*ModuleSource{
		{Kind: ModuleSourceKindGit, Git: &GitModuleSource{}},
		{Kind: ModuleSourceKindDir, DirSrc: &DirModuleSource{}},
		{Kind: ModuleSourceKindGit},
		{Kind: ModuleSourceKindDir},
		{Kind: ModuleSourceKindLocal},
		{Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{Foreign: true, ContextDirectoryPath: "/src"}},
		{Kind: ModuleSourceKind("")},
	} {
		read, err := moduleTreeReader(ctx, nil, src)
		require.NoError(t, err)
		require.Nil(t, read, src.Kind)
	}
}

// gitClientsTestTrees serves module trees from memory through the Directory
// and File fields a declaration read selects, counting the files it reads.
// Only exists and contents skip the cache, so every read reaches them: a
// File result must be cached.
type gitClientsTestTrees struct {
	mu    sync.Mutex
	trees map[*Directory]map[string]string
	files map[*File]string
	reads int
}

func installGitClientsTestTrees(srv *dagql.Server) *gitClientsTestTrees {
	trees := &gitClientsTestTrees{trees: map[*Directory]map[string]string{}, files: map[*File]string{}}
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{Typed: &Directory{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*File]{Typed: &File{}}))
	ExistsTypes.Install(srv)
	dagql.Fields[*Directory]{
		dagql.Func("exists", func(_ context.Context, self *Directory, args struct {
			Path         string
			ExpectedType dagql.Optional[ExistsType]
		}) (dagql.Boolean, error) {
			trees.mu.Lock()
			defer trees.mu.Unlock()
			_, ok := trees.trees[self][args.Path]
			return dagql.Boolean(ok), nil
		}).DoNotCache("counts every read"),
		dagql.Func("file", func(_ context.Context, self *Directory, args struct{ Path string }) (*File, error) {
			trees.mu.Lock()
			defer trees.mu.Unlock()
			file := &File{}
			trees.files[file] = trees.trees[self][args.Path]
			return file, nil
		}),
	}.Install(srv)
	dagql.Fields[*File]{
		dagql.Func("contents", func(_ context.Context, self *File, _ struct{}) (dagql.String, error) {
			trees.mu.Lock()
			defer trees.mu.Unlock()
			trees.reads++
			return dagql.String(trees.files[self]), nil
		}).DoNotCache("counts every read"),
	}.Install(srv)
	return trees
}

type gitClientsTestServer struct {
	*mockServer
	memo *SchemaBuilderMemo
}

func (s *gitClientsTestServer) SessionSchemaBuilderMemo(context.Context) (*SchemaBuilderMemo, error) {
	return s.memo, nil
}

func gitClientsTestConfig(client string) string {
	return fmt.Sprintf(`[modules.go]
source = "github.com/acme/go"

[sdks.go]
module = "go"

[sdks.go.scopes."modules/caller"]
is-module = true
clients = [%q]

[sdks.go.scopes."modules/other"]
is-module = true
clients = ["github.com/acme/private/other-scope"]
`, client)
}

// A git or directory module's declarations are read once per tree in a
// session, however many of its calls ask; a tree with an edited dagger.toml
// is read for its own.
func TestModuleDeclaredGitClientsReadOncePerTree(t *testing.T) {
	ctx := t.Context()
	server := &gitClientsTestServer{mockServer: &mockServer{}, memo: NewSchemaBuilderMemo()}
	query := &Query{Server: server}
	srv := newCoreDagqlServerForTest(t, query)
	trees := installGitClientsTestTrees(srv)
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	const session = "git-clients"
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: session, SessionID: session})
	ctx = ContextWithQuery(dagql.ContextWithCache(ctx, cache), query)
	t.Cleanup(func() { _ = cache.ReleaseSession(context.Background(), session) })

	tree := func(op string, files map[string]string) dagql.ObjectResult[*Directory] {
		t.Helper()
		dir := &Directory{}
		trees.mu.Lock()
		trees.trees[dir] = files
		trees.mu.Unlock()
		call := &dagql.ResultCall{Kind: dagql.ResultCallKindSynthetic, SyntheticOp: op, Type: dagql.NewResultCallType(dir.Type())}
		detached, err := dagql.NewObjectResultForCall(dir, srv, call)
		require.NoError(t, err)
		attached, err := cache.GetOrInitCall(ctx, session, srv, &dagql.CallRequest{ResultCall: call}, dagql.ValueFunc(detached))
		require.NoError(t, err)
		return attached.(dagql.ObjectResult[*Directory])
	}
	dirModuleAt := func(tree dagql.ObjectResult[*Directory], moduleDir string) *ModuleSource {
		return &ModuleSource{Kind: ModuleSourceKindDir, SourceRootSubpath: moduleDir, DirSrc: &DirModuleSource{OriginalContextDir: tree}}
	}
	dirModule := func(tree dagql.ObjectResult[*Directory]) *ModuleSource {
		return dirModuleAt(tree, "modules/caller")
	}
	gitModule := func(tree dagql.ObjectResult[*Directory]) *ModuleSource {
		return &ModuleSource{Kind: ModuleSourceKindGit, SourceRootSubpath: "modules/caller", Git: &GitModuleSource{UnfilteredContextDir: tree}}
	}
	declared := func(src *ModuleSource) []string {
		t.Helper()
		clients, err := ModuleDeclaredGitClients(ctx, srv, src)
		require.NoError(t, err)
		return clients
	}

	original := tree("original", map[string]string{"dagger.toml": gitClientsTestConfig("github.com/acme/private/hello")})
	for range 5 {
		require.Equal(t, []string{"github.com/acme/private/hello"}, declared(dirModule(original)))
	}
	require.Equal(t, 1, trees.reads)

	// Loaded from git, the same tree is the same declarations.
	require.Equal(t, []string{"github.com/acme/private/hello"}, declared(gitModule(original)))
	require.Equal(t, 1, trees.reads)

	// Another module in the same tree has its own.
	require.Equal(t, []string{"github.com/acme/private/other-scope"}, declared(dirModuleAt(original, "modules/other")))
	require.Equal(t, []string{"github.com/acme/private/other-scope"}, declared(dirModuleAt(original, "modules/other")))
	require.Equal(t, 2, trees.reads)

	// An edited dagger.toml is a new tree, read for its own declarations.
	edited := tree("edited", map[string]string{"dagger.toml": gitClientsTestConfig("github.com/acme/private/other")})
	for range 5 {
		require.Equal(t, []string{"github.com/acme/private/other"}, declared(dirModule(edited)))
	}
	require.Equal(t, 3, trees.reads)
	require.Equal(t, []string{"github.com/acme/private/hello"}, declared(dirModule(original)))
	require.Equal(t, 3, trees.reads)

	// Another session reads again.
	server.memo = NewSchemaBuilderMemo()
	require.Equal(t, []string{"github.com/acme/private/hello"}, declared(dirModule(original)))
	require.Equal(t, 4, trees.reads)
}

func TestDeclaredGitClientsRetriesFailedRead(t *testing.T) {
	memo := NewSchemaBuilderMemo()
	key := declaredGitClientsKey{treeID: 1, moduleDir: "."}
	reads := 0
	failing := func() ([]string, error) {
		reads++
		return nil, fmt.Errorf("read failed")
	}
	_, err := memo.declaredGitClients(key, failing)
	require.Error(t, err)
	declared, err := memo.declaredGitClients(key, func() ([]string, error) {
		reads++
		return []string{"github.com/acme/private/hello"}, nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"github.com/acme/private/hello"}, declared)
	_, err = memo.declaredGitClients(key, failing)
	require.NoError(t, err)
	require.Equal(t, 2, reads)
}
