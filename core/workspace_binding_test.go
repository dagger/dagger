package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestNestedClientMetadataForInheritedWorkspace(t *testing.T) {
	t.Parallel()

	dag, err := dagql.NewServer(t.Context(), &Workspace{})
	require.NoError(t, err)
	workspace, err := dagql.NewObjectResultForCall(
		&Workspace{Address: "test://workspace"},
		dag,
		&dagql.ResultCall{SyntheticOp: "inherited-workspace"},
	)
	require.NoError(t, err)

	clientMetadata := &engine.ClientMetadata{
		SessionID:         "session",
		AllowedLLMModules: []string{"llm"},
		LockMode:          "frozen",
	}
	binding := &InheritedWorkspaceBinding{
		Workspace:       workspace,
		BindingID:       "binding",
		WorkspaceEnv:    "ci",
		HasWorkspaceEnv: true,
	}

	nested := nestedClientMetadataForExec(clientMetadata, nil, false, binding)
	require.NotNil(t, nested)
	require.Equal(t, "session", nested.SessionID)
	require.Equal(t, []string{"llm"}, nested.AllowedLLMModules)
	require.Equal(t, "frozen", nested.LockMode)
	require.Equal(t, "binding", nested.InheritedWorkspaceBindingID)
	require.Equal(t, "ci", nested.InheritedWorkspaceEnv)
	require.True(t, nested.InheritedWorkspaceEnvSet)

	require.NotNil(t, nestedClientMetadataForExec(clientMetadata, nil, true, nil))
	require.Nil(t, nestedClientMetadataForExec(clientMetadata, nil, false, nil))
}

func TestInheritedWorkspacePersistence(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)

	query := &Query{Server: &cacheVolumeTestQueryServer{mockServer: &mockServer{}}}
	srv := newCoreDagqlServerForTest(t, query)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Workspace]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Container]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Service]{}))

	workspace := volumeTestCachedObjectResult(t, ctx, cache, srv, "workspace-session", "workspace", &Workspace{
		Address: "test://inherited-workspace",
	})
	parent := volumeTestCachedObjectResult(t, ctx, cache, srv, "workspace-session", "parent", NewContainer(Platform(specs.Platform{
		OS:           "linux",
		Architecture: "amd64",
	})))
	parentResultID, err := cache.PersistedResultID(parent)
	require.NoError(t, err)
	binding := &InheritedWorkspaceBinding{
		Workspace:       workspace,
		BindingID:       "binding-id",
		WorkspaceEnv:    "ci",
		HasWorkspaceEnv: true,
	}

	t.Run("container exec lazy", func(t *testing.T) {
		container := NewContainer(parent.Self().Platform)
		container.Lazy = &ContainerExecLazy{State: &ContainerExecState{
			LazyState:          NewLazyState(),
			Parent:             parent,
			Opts:               ContainerExecOpts{Args: []string{"true"}},
			InheritedWorkspace: binding,
		}}

		encoded, err := container.EncodePersistedObject(ctx, cache)
		require.NoError(t, err)
		var payload persistedContainerPayload
		require.NoError(t, json.Unmarshal(encoded.JSON, &payload))
		var lazyPayload persistedContainerExecLazy
		require.NoError(t, json.Unmarshal(payload.LazyJSON, &lazyPayload))
		require.NotZero(t, lazyPayload.InheritedWorkspaceResultID)
		require.Equal(t, "binding-id", lazyPayload.InheritedWorkspaceBindingID)
		require.Equal(t, "ci", lazyPayload.InheritedWorkspaceEnv)
		require.True(t, lazyPayload.InheritedWorkspaceEnvSet)

		call := volumeTestCall("container-with-exec", container)
		call.Field = "withExec"
		decodedTyped, err := (&Container{}).DecodePersistedObject(ctx, srv, parentResultID, call, encoded.JSON)
		require.NoError(t, err)
		decoded := decodedTyped.(*Container)
		lazy := decoded.Lazy.(*ContainerExecLazy)
		require.Equal(t, "test://inherited-workspace", lazy.State.InheritedWorkspace.Workspace.Self().Address)
		require.Equal(t, "binding-id", lazy.State.InheritedWorkspace.BindingID)
		require.Equal(t, "ci", lazy.State.InheritedWorkspace.WorkspaceEnv)
		require.True(t, lazy.State.InheritedWorkspace.HasWorkspaceEnv)
	})

	t.Run("service", func(t *testing.T) {
		service := &Service{
			Container:          parent,
			Args:               []string{"true"},
			InheritedWorkspace: binding,
		}
		encoded, err := service.EncodePersistedObject(ctx, cache)
		require.NoError(t, err)
		var payload persistedServicePayload
		require.NoError(t, json.Unmarshal(encoded.JSON, &payload))
		require.NotZero(t, payload.InheritedWorkspaceResultID)
		require.Equal(t, "binding-id", payload.InheritedWorkspaceBindingID)
		require.Equal(t, "ci", payload.InheritedWorkspaceEnv)
		require.True(t, payload.InheritedWorkspaceEnvSet)

		decodedTyped, err := (&Service{}).DecodePersistedObject(ctx, srv, 0, nil, encoded.JSON)
		require.NoError(t, err)
		decoded := decodedTyped.(*Service)
		require.Equal(t, "test://inherited-workspace", decoded.InheritedWorkspace.Workspace.Self().Address)
		require.Equal(t, "binding-id", decoded.InheritedWorkspace.BindingID)
		require.Equal(t, "ci", decoded.InheritedWorkspace.WorkspaceEnv)
		require.True(t, decoded.InheritedWorkspace.HasWorkspaceEnv)
	})
}

func TestSchemaBuilderMergePrefersExistingModuleAndPromotesInstallPolicy(t *testing.T) {
	t.Parallel()

	dag, err := dagql.NewServer(t.Context(), &Module{})
	require.NoError(t, err)
	left, err := dagql.NewObjectResultForCall(
		&Module{NameField: "greeter"},
		dag,
		&dagql.ResultCall{SyntheticOp: "left-greeter"},
	)
	require.NoError(t, err)
	right, err := dagql.NewObjectResultForCall(
		&Module{NameField: "greeter"},
		dag,
		&dagql.ResultCall{SyntheticOp: "right-greeter"},
	)
	require.NoError(t, err)

	merged := NewSchemaBuilder(nil, nil).
		With(NewUserMod(left), InstallOpts{SkipConstructor: true}).
		Merge(NewSchemaBuilder(nil, nil).
			With(NewUserMod(right), InstallOpts{Entrypoint: true}))

	require.Len(t, merged.entries, 1)
	require.Same(t, left.Self(), merged.entries[0].mod.ModuleResult().Self())
	require.False(t, merged.entries[0].opts.SkipConstructor)
	require.True(t, merged.entries[0].opts.Entrypoint)
}
