package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// approvedSecretServer approves reads of the owner's secrets whose URIs are in
// approved, as Server.AuthorizeSecret does once the owner says yes.
type approvedSecretServer struct {
	*llmAuthSchemaServer
	owner    *engine.ClientMetadata
	approved map[string]bool
}

func (s *approvedSecretServer) AuthorizeSecret(_ context.Context, uri string) (*engine.ClientMetadata, error) {
	if s.approved[uri] {
		return s.owner, nil
	}
	return nil, nil
}

// An approved secret is read from the owner's host once, when it is minted,
// and its handle comes from its value even when the caller passes a cache
// key: a mint that was never approved cannot claim its handle.
func TestApprovedSecretReadsOnce(t *testing.T) {
	module := &engine.ClientMetadata{SessionID: "approved-secret-session", ClientID: "module"}
	owner := &engine.ClientMetadata{SessionID: module.SessionID, ClientID: "owner"}
	const uri = "cmd://gh auth token"
	host := &llmAuthSecrets{vars: map[string]string{uri: "owner-token"}}
	srv := &approvedSecretServer{
		llmAuthSchemaServer: &llmAuthSchemaServer{
			currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: owner},
			parent:                    owner,
			attachables:               map[string]*grpc.ClientConn{owner.ClientID: newLLMAuthAttachable(t, host)},
		},
		owner:    owner,
		approved: map[string]bool{uri: true},
	}
	ctx := engine.ContextWithClientMetadata(t.Context(), module)
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	root := core.NewRoot(srv)
	ctx = core.ContextWithQuery(ctx, root)
	base, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	dag, err := base.Fork(ctx, root, "")
	require.NoError(t, err)
	srv.dag = dag

	mint := func(uri string) dagql.ObjectResult[*core.Secret] {
		var secret dagql.ObjectResult[*core.Secret]
		require.NoError(t, dag.Select(ctx, dag.Root(), &secret, dagql.Selector{
			Field: "secret",
			Args: []dagql.NamedInput{
				{Name: "uri", Value: dagql.NewString(uri)},
				{Name: "cacheKey", Value: dagql.Opt(dagql.NewString("shared-key"))},
			},
		}))
		return secret
	}
	approved := mint(uri)
	for range 2 {
		var plaintext string
		require.NoError(t, dag.Select(ctx, approved, &plaintext, dagql.Selector{Field: "plaintext"}))
		require.Equal(t, "owner-token", plaintext)
	}
	host.mu.Lock()
	reads := host.reads
	host.mu.Unlock()
	require.Equal(t, 1, reads, "one approval reads the owner's host once")
	require.Equal(t, core.SecretHandleFromPlaintext(srv.SecretSalt(), []byte("owner-token")), approved.Self().Handle)

	unapproved := mint("env://UNAPPROVED")
	require.Equal(t, core.SecretHandleFromCacheKey("shared-key"), unapproved.Self().Handle)
	require.NotEqual(t, approved.Self().Handle, unapproved.Self().Handle,
		"a cache key cannot give an unapproved mint the approved secret's handle")
}
