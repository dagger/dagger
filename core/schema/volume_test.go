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
