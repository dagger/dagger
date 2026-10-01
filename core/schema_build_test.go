package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

type artifactCoreForkTestMod struct {
	Mod
	base *dagql.Server
	view call.View
}

func (*artifactCoreForkTestMod) Name() string              { return ModuleName }
func (m *artifactCoreForkTestMod) View() (call.View, bool) { return m.view, true }
func (*artifactCoreForkTestMod) Install(context.Context, *dagql.Server, ...InstallOpts) error {
	panic("artifact schemas must fork the prepared core")
}
func (m *artifactCoreForkTestMod) ForkSchema(ctx context.Context, root *Query, view call.View) (*dagql.Server, error) {
	srv, err := m.base.Fork(ctx, root)
	if err == nil {
		srv.View = view
	}
	return srv, err
}

type artifactProbeMod struct {
	Mod
	installs int
}

func (*artifactProbeMod) Name() string            { return "probe" }
func (*artifactProbeMod) View() (call.View, bool) { return "", false }
func (m *artifactProbeMod) Install(_ context.Context, dag *dagql.Server, _ ...InstallOpts) error {
	m.installs++
	dagql.Fields[*Query]{dagql.Func("probe", func(context.Context, *Query, struct{}) (dagql.String, error) {
		return "probe", nil
	})}.Install(dag)
	return nil
}

type artifactCoreForkTestServer struct {
	*mockServer
	deps *SchemaBuilder
}

func (s *artifactCoreForkTestServer) DefaultDeps(context.Context) (*SchemaBuilder, error) {
	return s.deps, nil
}

// An artifact server forks the prepared core schema instead of installing
// it again. Each server gets its own copy: installing a module into one
// changes neither the core template nor another server.
func TestArtifactSchemaForkViewAndIsolation(t *testing.T) {
	server := &artifactCoreForkTestServer{mockServer: &mockServer{}}
	root := &Query{Server: server}
	ctx := ContextWithQuery(t.Context(), root)
	base, err := dagql.NewServer(ctx, root)
	require.NoError(t, err)
	coreMod := &artifactCoreForkTestMod{base: base, view: "v0.21.0"}
	probe := &artifactProbeMod{}
	server.deps = NewSchemaBuilder(root, []Mod{coreMod, probe}).ClientOwned()
	base.InstallObject(dagql.NewClass(base, dagql.ClassOpts[*Module]{}))
	module := &Module{NameField: "fixture"}
	mod, err := dagql.NewObjectResultForCall(module, base, &dagql.ResultCall{
		Field: "fixture", Type: dagql.NewResultCallType(module.Type()),
	})
	require.NoError(t, err)
	var forks []*dagql.Server
	for range 2 {
		callerRoot := &Query{Server: root.Server}
		fork, err := dagqlServerForModule(ContextWithQuery(ctx, callerRoot), mod)
		require.NoError(t, err)
		require.Empty(t, fork.View, "artifact view must not inherit the authored module version")
		require.Same(t, callerRoot, fork.Root().Unwrap())
		_, exists := fork.Root().ObjectType().FieldSpec("probe", "")
		require.True(t, exists)
		forks = append(forks, fork)
	}
	// The existing version-selecting path is unchanged.
	versioned, err := buildSchema(ctx, root, []modInstall{{mod: coreMod}, {mod: probe}})
	require.NoError(t, err)
	require.Equal(t, call.View("v0.21.0"), versioned.View)
	require.Equal(t, 2, probe.installs)
	_, exists := base.Root().ObjectType().FieldSpec("probe", "")
	require.False(t, exists, "module installation must not modify the core template")
	dagql.Fields[*Query]{dagql.Func("firstOnly", func(context.Context, *Query, struct{}) (string, error) {
		return "first", nil
	})}.Install(forks[0])
	_, exists = forks[1].Root().ObjectType().FieldSpec("firstOnly", "")
	require.False(t, exists)
	template, err := server.deps.Schema(ctx)
	require.NoError(t, err)
	require.Equal(t, call.View("v0.21.0"), template.View)
	_, exists = template.Root().ObjectType().FieldSpec("firstOnly", "")
	require.False(t, exists, "artifact mutations must not change the cached schema")
}

func TestSchemaJSONFileSelectorHiddenFieldsAffectCallIdentity(t *testing.T) {
	hiddenTypes, hiddenFields := moduleIntrospectionScrubConfig()
	clientSelector := schemaJSONFileSelector("v1.0.0", nil, nil)
	moduleSelector := schemaJSONFileSelector("v1.0.0", hiddenTypes, hiddenFields)

	require.Equal(t, []string{
		"Query.currentWorkspace",
		"Query.engineVolume",
		"Query.sshfsVolume",
		"Query.setSessionTitle",
		"Address.volume",
	}, hiddenFields)
	require.Contains(t, hiddenTypes, "Host")
	require.NotEqual(t, selectorCallID(clientSelector).Digest(), selectorCallID(moduleSelector).Digest())

	hiddenFieldsInput, ok := dagql.Inputs(moduleSelector.Args).Lookup("hiddenFields")
	require.True(t, ok)
	require.Equal(t, `["Query.currentWorkspace","Query.engineVolume","Query.sshfsVolume","Query.setSessionTitle","Address.volume"]`, hiddenFieldsInput.ToLiteral().Display())
}

func selectorCallID(selector dagql.Selector) *call.ID {
	args := make([]*call.Argument, 0, len(selector.Args))
	for _, arg := range selector.Args {
		args = append(args, call.NewArgument(arg.Name, arg.Value.ToLiteral(), false))
	}
	return call.New().Append(
		&ast.Type{NamedType: "File", NonNull: true},
		selector.Field,
		call.WithArgs(args...),
		call.WithView(selector.View),
	)
}
