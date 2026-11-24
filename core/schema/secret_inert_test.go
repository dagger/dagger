package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// inertSecretServer serves the host client's attachables and denies every
// other client's, as the session denies a host-inert client's.
type inertSecretServer struct {
	*currentTypeDefsTestServer
	conns map[string]*grpc.ClientConn
}

func (s *inertSecretServer) SpecificClientAttachableConn(_ context.Context, clientID string, _ core.SpecificClientAttachableConnOpts) (*grpc.ClientConn, bool, error) {
	if conn, ok := s.conns[clientID]; ok {
		return conn, true, nil
	}
	return nil, false, status.Error(codes.PermissionDenied, "SDK client access to host session attachables is denied")
}

// A recipe holding a host secret can be replayed first by a client with no
// host: a host-inert client rebuilding an agent's tool object from its ID
// (a worker's tools, a DangEval snippet after a resume). The replay must
// load, since most such objects never read the secret there, and must not
// leave a result behind that keeps the host client from reading it next.
func TestInertClientSecretReplay(t *testing.T) {
	const session = "inert-secret"
	host := &engine.ClientMetadata{SessionID: session, ClientID: "host"}
	snippet := &engine.ClientMetadata{SessionID: session, ClientID: "snippet", InertAttachables: true}
	env := &llmAuthSecrets{vars: map[string]string{"env://TOKEN": "from-the-host"}}
	srv := &inertSecretServer{
		currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: host, platform: core.Platform{OS: "linux", Architecture: "amd64"}},
		conns:                     map[string]*grpc.ClientConn{host.ClientID: newLLMAuthAttachable(t, env)},
	}

	newCache := func() *dagql.Cache {
		cache, err := dagql.NewCache(t.Context(), "", nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
		return cache
	}
	var newLease func(engine.ClientLeaseKind, string) *engine.ClientLifecycleLease
	newLease = func(kind engine.ClientLeaseKind, owner string) *engine.ClientLifecycleLease {
		return engine.NewClientLifecycleLease(kind, owner, nil, func(kind engine.ClientLeaseKind, owner string) (*engine.ClientLifecycleLease, error) {
			return newLease(kind, owner), nil
		})
	}
	as := func(cache *dagql.Cache, md *engine.ClientMetadata) (context.Context, *dagql.Server) {
		lease := newLease(engine.ClientLeaseRequest, md.ClientID)
		t.Cleanup(lease.Release)
		scope, err := engine.NewClientScope(md, lease)
		require.NoError(t, err)
		ctx, err := engine.ContextWithClientScope(t.Context(), scope)
		require.NoError(t, err)
		ctx = dagql.ContextWithCache(ctx, cache)
		root := core.NewRoot(srv)
		ctx = core.ContextWithQuery(ctx, root)
		base, err := NewCoreSchemaBase(ctx, srv)
		require.NoError(t, err)
		dag, err := base.Fork(ctx, root, "")
		require.NoError(t, err)
		srv.dag = dag
		srv.deps = core.NewSchemaBuilder(root, nil)
		return ctx, dag
	}

	// The agent made the recipe, in an earlier engine: an object holding a
	// host secret, as a tool object's constructor arguments do.
	ctx, dag := as(newCache(), host)
	var made dagql.ObjectResult[*core.Secret]
	require.NoError(t, dag.Select(ctx, dag.Root(), &made, dagql.Selector{
		Field: "secret",
		Args:  []dagql.NamedInput{{Name: "uri", Value: dagql.NewString("env://TOKEN")}},
	}))
	secretRecipe, err := made.RecipeID(ctx)
	require.NoError(t, err)
	var holder dagql.ObjectResult[*core.Container]
	require.NoError(t, dag.Select(ctx, dag.Root(), &holder,
		dagql.Selector{Field: "container"},
		dagql.Selector{Field: "withSecretVariable", Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString("TOKEN")},
			{Name: "secret", Value: dagql.NewID[*core.Secret](secretRecipe)},
		}},
	))
	recipe, err := holder.RecipeID(ctx)
	require.NoError(t, err)

	// After a resume, the host-less client replays it first: it loads, though
	// it couldn't read the secret.
	resumed := newCache()
	ctx, dag = as(resumed, snippet)
	_, err = dagql.NewID[*core.Container](recipe).Load(ctx, dag)
	require.NoError(t, err, "a tool object holding a host secret must load in a host-less client")

	// The agent replays it next, and reads it from its host.
	ctx, dag = as(resumed, host)
	loaded, err := dagql.NewID[*core.Container](recipe).Load(ctx, dag)
	require.NoError(t, err)
	require.Len(t, loaded.Self().Secrets, 1)
	plaintext, err := loaded.Self().Secrets[0].Secret.Self().Plaintext(ctx)
	require.NoError(t, err)
	require.Equal(t, "from-the-host", string(plaintext))
}
