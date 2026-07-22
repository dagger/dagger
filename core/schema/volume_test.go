package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestParseSSHFSVolumeEndpoint(t *testing.T) {
	endpoint, hostAlias, err := parseSSHFSVolumeEndpoint("sshfs://git@example.com:2222/srv/repo")
	require.NoError(t, err)
	require.Equal(t, "sshfs://git@example.com:2222/srv/repo", endpoint)
	require.Equal(t, "[example.com]:2222", hostAlias)

	_, hostAlias, err = parseSSHFSVolumeEndpoint("sshfs://git@example.com:22/srv/repo")
	require.NoError(t, err)
	require.Equal(t, "example.com", hostAlias)

	for _, endpoint := range []string{
		"sshfs://git:hunter2@example.com/srv/repo",
		"sshfs://git:@example.com/srv/repo",
	} {
		_, _, err := parseSSHFSVolumeEndpoint(endpoint)
		require.EqualError(t, err, "SSHFS endpoint must not include a password")
		require.NotContains(t, err.Error(), "hunter2")
	}

	for _, tc := range []string{
		"",
		"ssh://git@example.com/srv/repo",
		"sshfs:///srv/repo",
		"sshfs://example.com",
		"sshfs://example.com/srv/repo",
		"sshfs://git@example.com/srv/repo?cacheKey=x",
		"sshfs://example.com/srv/repo#fragment",
	} {
		_, _, err := parseSSHFSVolumeEndpoint(tc)
		require.Error(t, err, tc)
	}
}

func TestVolumeContentDigestFromCacheKey(t *testing.T) {
	require.Equal(t,
		core.VolumeContentDigestFromCacheKey("shared"),
		core.VolumeContentDigestFromCacheKey("shared"),
	)
	require.NotEqual(t,
		core.VolumeContentDigestFromCacheKey("shared"),
		core.VolumeContentDigestFromCacheKey("other"),
	)
}

func TestEngineVolumeRequiresMainClient(t *testing.T) {
	ctx, dag := newVolumeSchemaTestDag(t, &engine.ClientMetadata{
		ClientID:  "main",
		SessionID: "engine-volume-gate-session",
	})
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "nested",
		SessionID: "engine-volume-gate-session",
	})

	var vol dagql.ObjectResult[*core.Volume]
	err := dag.Select(ctx, dag.Root(), &vol, dagql.Selector{
		Field: "engineVolume",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString("datasets/models")},
		},
	})
	require.ErrorContains(t, err, "only the main client")
}

func TestEngineVolumeConstructsStableLogicalConfig(t *testing.T) {
	metadata := &engine.ClientMetadata{
		ClientID:  "main",
		SessionID: "engine-volume-constructor-session",
	}
	ctx, dag := newVolumeSchemaTestDag(t, metadata)

	var first dagql.ObjectResult[*core.Volume]
	err := dag.Select(ctx, dag.Root(), &first, dagql.Selector{
		Field: "engineVolume",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString("datasets/models")},
			{Name: "subdir", Value: dagql.Optional[dagql.String]{Valid: true, Value: "llama/weights"}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, core.VolumeBackendKindEngine, first.Self().Backend)
	require.Equal(t, &core.EngineVolumeConfig{
		Name:          "datasets/models",
		Subdir:        "llama/weights",
		LayoutVersion: core.EngineVolumeLayoutVersion,
	}, first.Self().Engine)

	firstID, err := first.ID()
	require.NoError(t, err)
	secondMetadata := &engine.ClientMetadata{
		ClientID:  "main-2",
		SessionID: "engine-volume-constructor-session-2",
	}
	ctx = engine.ContextWithClientMetadata(ctx, secondMetadata)
	root, ok := dag.Root().(dagql.ObjectResult[*core.Query])
	require.True(t, ok)
	root.Self().Server.(*currentTypeDefsTestServer).mainClient = secondMetadata
	var second dagql.ObjectResult[*core.Volume]
	err = dag.Select(ctx, dag.Root(), &second, dagql.Selector{
		Field: "engineVolume",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString("datasets/models")},
			{Name: "subdir", Value: dagql.Optional[dagql.String]{Valid: true, Value: "llama/weights"}},
		},
	})
	require.NoError(t, err)
	secondID, err := second.ID()
	require.NoError(t, err)
	require.Equal(t, firstID, secondID)
}

func TestEngineVolumeRejectsInvalidPaths(t *testing.T) {
	ctx, dag := newVolumeSchemaTestDag(t, &engine.ClientMetadata{
		ClientID:  "main",
		SessionID: "engine-volume-validation-session",
	})

	for _, tc := range []struct {
		name   string
		subdir dagql.Optional[dagql.String]
	}{
		{name: "../escape"},
		{name: "group/fs"},
		{name: "group/data", subdir: dagql.Optional[dagql.String]{Valid: true, Value: "../escape"}},
		{name: "group/data", subdir: dagql.Optional[dagql.String]{Valid: true, Value: ""}},
	} {
		args := []dagql.NamedInput{{Name: "name", Value: dagql.NewString(tc.name)}}
		if tc.subdir.Valid {
			args = append(args, dagql.NamedInput{Name: "subdir", Value: tc.subdir})
		}
		var vol dagql.ObjectResult[*core.Volume]
		err := dag.Select(ctx, dag.Root(), &vol, dagql.Selector{Field: "engineVolume", Args: args})
		require.Error(t, err, tc.name)
	}
}

func TestSSHFSVolumeRequiresMainClient(t *testing.T) {
	ctx, dag := newVolumeSchemaTestDag(t, &engine.ClientMetadata{
		ClientID:  "main",
		SessionID: "volume-gate-session",
	})
	privateKey := newTestSecret(t, ctx, dag)
	privateKeyID, err := privateKey.ID()
	require.NoError(t, err)

	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "nested",
		SessionID: "volume-gate-session",
	})

	var vol dagql.ObjectResult[*core.Volume]
	err = dag.Select(ctx, dag.Root(), &vol, dagql.Selector{
		Field: "sshfsVolume",
		Args: []dagql.NamedInput{
			{Name: "endpoint", Value: dagql.NewString("sshfs://git@example.com/srv/repo")},
			{Name: "privateKey", Value: dagql.NewID[*core.Secret](privateKeyID)},
			{Name: "insecureSkipHostKeyCheck", Value: dagql.Boolean(true)},
		},
	})
	require.ErrorContains(t, err, "only the main client")
}

func TestAddressVolumeRequiresMainClient(t *testing.T) {
	ctx, dag := newVolumeSchemaTestDag(t, &engine.ClientMetadata{
		ClientID:  "main",
		SessionID: "volume-address-gate-session",
	})
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "nested",
		SessionID: "volume-address-gate-session",
	})

	var vol dagql.ObjectResult[*core.Volume]
	err := dag.Select(ctx, dag.Root(), &vol,
		dagql.Selector{
			Field: "address",
			Args: []dagql.NamedInput{
				{Name: "value", Value: dagql.NewString("sshfs://git@example.com/srv/repo?privateKey=env://SSH_KEY&insecureSkipHostKeyCheck=true")},
			},
		},
		dagql.Selector{
			Field: "volume",
		},
	)
	require.ErrorContains(t, err, "only the main client")
}

func TestSSHFSVolumeRequiresKnownHostsUnlessInsecure(t *testing.T) {
	ctx, dag := newVolumeSchemaTestDag(t, &engine.ClientMetadata{
		ClientID:  "main",
		SessionID: "volume-known-hosts-session",
	})
	privateKey := newTestSecret(t, ctx, dag)
	privateKeyID, err := privateKey.ID()
	require.NoError(t, err)

	var vol dagql.ObjectResult[*core.Volume]
	err = dag.Select(ctx, dag.Root(), &vol, dagql.Selector{
		Field: "sshfsVolume",
		Args: []dagql.NamedInput{
			{Name: "endpoint", Value: dagql.NewString("sshfs://git@example.com/srv/repo")},
			{Name: "privateKey", Value: dagql.NewID[*core.Secret](privateKeyID)},
		},
	})
	require.ErrorContains(t, err, "knownHosts is required")
}

func newVolumeSchemaTestDag(t *testing.T, metadata *engine.ClientMetadata) (context.Context, *dagql.Server) {
	t.Helper()

	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, metadata)

	srv := &currentTypeDefsTestServer{mainClient: metadata}
	root := core.NewRoot(srv)
	coreSchemaBase, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	dag, err := coreSchemaBase.Fork(ctx, root, "")
	require.NoError(t, err)
	srv.dag = dag
	return ctx, dag
}

func newTestSecret(t *testing.T, ctx context.Context, dag *dagql.Server) dagql.ObjectResult[*core.Secret] {
	t.Helper()

	var secret dagql.ObjectResult[*core.Secret]
	err := dag.Select(ctx, dag.Root(), &secret, dagql.Selector{
		Field: "setSecret",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString("volume-test-key")},
			{Name: "plaintext", Value: dagql.NewString("test-private-key")},
		},
	})
	require.NoError(t, err)
	return secret
}
