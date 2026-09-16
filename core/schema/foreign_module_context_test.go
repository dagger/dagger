package schema

import (
	"context"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestForeignModuleContextReaders(t *testing.T) {
	s := &moduleSourceSchema{}
	native := &core.ModuleSource{Kind: core.ModuleSourceKindLocal, SourceRootSubpath: ".", Local: &core.LocalModuleSource{ContextDirectoryPath: "/producer"}}
	foreign := native.Clone()
	foreign.Local.Foreign = true
	for _, pair := range [][2]*core.ModuleSource{{foreign, native}, {native, foreign}, {foreign, foreign}} {
		_, err := s.moduleConfigDependencyForRelatedSource(pair[0], pair[1])
		require.ErrorIs(t, err, core.ErrForeignModuleContext)
	}
	_, err := s.moduleConfigDependencyForRelatedSource(native, native)
	require.NoError(t, err)
	text, err := s.moduleSourceAsString(t.Context(), foreign, struct{}{})
	require.NoError(t, err)
	require.Equal(t, "/producer", text)
	_, err = s.moduleSourceWithSourceSubpath(t.Context(), foreign, struct{ Path string }{Path: "../escape"})
	require.ErrorContains(t, err, "escapes")
}
func TestForeignLocalItemRemoval(t *testing.T) {
	srv, err := dagql.NewServer(t.Context(), &core.Query{})
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.ModuleSource]{Typed: &core.ModuleSource{}}))
	parent := &core.ModuleSource{Kind: core.ModuleSourceKindLocal, SourceRootSubpath: ".", Local: &core.LocalModuleSource{Foreign: true, ContextDirectoryPath: "/producer"}}
	child := parent.Clone()
	child.SourceRootSubpath = "dep"
	child.ModuleName = "dep"
	parent.Dependencies = dagql.ObjectResultArray[*core.ModuleSource]{moduleSourceObjectResult(t, srv, "dep", child)}
	result, err := (&moduleSourceSchema{}).moduleSourceWithoutDependencies(t.Context(), parent, struct{ Dependencies []string }{[]string{"./dep"}})
	require.NoError(t, err)
	require.Empty(t, result.Dependencies)
	require.True(t, result.Local.Foreign)
	require.Len(t, parent.Dependencies, 1)
	_, err = result.LocalContextDirectoryPath()
	require.ErrorIs(t, err, core.ErrForeignModuleContext)
}

type foreignWorkspaceTestServer struct{ *currentTypeDefsTestServer }

func (s *foreignWorkspaceTestServer) NonModuleParentClientMetadata(context.Context) (*engine.ClientMetadata, error) {
	return &engine.ClientMetadata{ClientID: "workspace", SessionID: "workspace"}, nil
}

func TestForeignWithSourceSubpathWorkspace(t *testing.T) {
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: "workspace", SessionID: "workspace"})
	cache, err := dagql.NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	facade := &foreignWorkspaceTestServer{&currentTypeDefsTestServer{}}
	q := core.NewRoot(facade)
	ctx = core.ContextWithQuery(dagql.ContextWithCache(ctx, cache), q)
	srv, err := dagql.NewServer(ctx, q)
	require.NoError(t, err)
	facade.dag = srv
	for _, class := range []dagql.ObjectType{dagql.NewClass(srv, dagql.ClassOpts[*core.Workspace]{}), dagql.NewClass(srv, dagql.ClassOpts[*core.Directory]{}), dagql.NewClass(srv, dagql.ClassOpts[*core.File]{}), dagql.NewClass(srv, dagql.ClassOpts[*core.EnvFile]{}), dagql.NewClass(srv, dagql.ClassOpts[*core.Host]{})} {
		srv.InstallObject(class)
	}
	directoryCalls := 0
	dagql.Fields[*core.Workspace]{
		dagql.NodeFunc("directory", func(ctx context.Context, _ dagql.ObjectResult[*core.Workspace], args struct {
			Path string
			core.CopyFilter
		}) (dagql.ObjectResult[*core.Directory], error) {
			require.Equal(t, "/", args.Path)
			directoryCalls++
			return dagql.NewObjectResultForCurrentCall(ctx, srv, &core.Directory{})
		}),
		dagql.Func("file", func(context.Context, *core.Workspace, struct{ Path string }) (*core.File, error) {
			return nil, os.ErrNotExist
		}),
	}.Install(srv)
	dagql.Fields[*core.File]{dagql.Func("asEnvFile", func(context.Context, *core.File, struct {
		Expand bool `default:"false"`
	}) (*core.EnvFile, error) { return nil, os.ErrNotExist })}.Install(srv)
	dagql.Fields[*core.Query]{dagql.Func("host", func(context.Context, *core.Query, struct{}) (*core.Host, error) { return &core.Host{}, nil })}.Install(srv)
	dagql.Fields[*core.Host]{dagql.Func("findUp", func(context.Context, *core.Host, struct{ Name string }) (string, error) { return "", nil })}.Install(srv)
	ws, err := dagql.NewObjectResultForCall(&core.Workspace{}, srv, &dagql.ResultCall{Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "workspace", Type: dagql.NewResultCallType((&core.Workspace{}).Type())})
	require.NoError(t, err)
	src := &core.ModuleSource{Kind: core.ModuleSourceKindLocal, ModuleName: "probe", SourceRootSubpath: ".", Local: &core.LocalModuleSource{Foreign: true, ContextDirectoryPath: "/producer/absent"}, Workspace: ws}
	got, err := (&moduleSourceSchema{}).moduleSourceWithSourceSubpath(ctx, src, struct{ Path string }{"code"})
	require.NoError(t, err)
	require.True(t, got.Local.Foreign)
	require.Equal(t, "code", got.SourceSubpath)
	require.Equal(t, 1, directoryCalls)
	require.NotNil(t, got.ContextDirectory.Self())
	got.Workspace = dagql.ObjectResult[*core.Workspace]{}
	_, err = got.LoadContextFile(ctx, srv, "notes.txt")
	require.ErrorIs(t, err, core.ErrForeignModuleContext)
}
