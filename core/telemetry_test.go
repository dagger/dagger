package core

import (
	"context"
	"net/http"
	"testing"

	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/filesync"
	"github.com/dagger/dagger/engine/server/resource"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/moby/locker"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

type mockServer struct {
}

func (ms *mockServer) ServeModule(ctx context.Context, mod *Module, includeDependencies bool) error {
	return nil
}
func (ms *mockServer) CurrentModule(context.Context) (*Module, error) {
	c := call.New().Append(&ast.Type{}, "caller1")
	rs, err := dagql.NewResultForID(&ModuleSource{}, c)
	if err != nil {
		panic(err)
	}

	dn := dagql.Nullable[dagql.ObjectResult[*ModuleSource]]{
		Valid: true,
		Value: dagql.ObjectResult[*ModuleSource]{Result: rs},
	}
	return &Module{
		Source: dn,
	}, nil
}
func (ms *mockServer) CurrentFunctionCall(context.Context) (*FunctionCall, error) {
	return nil, nil
}
func (ms *mockServer) CurrentServedDeps(context.Context) (*ModDeps, error) {
	return &ModDeps{}, nil
}
func (ms *mockServer) MainClientCallerMetadata(context.Context) (*engine.ClientMetadata, error) {
	return &engine.ClientMetadata{}, nil
}
func (ms *mockServer) NonModuleParentClientMetadata(context.Context) (*engine.ClientMetadata, error) {
	return nil, nil
}
func (ms *mockServer) DefaultDeps(context.Context) (*ModDeps, error)           { return nil, nil }
func (ms *mockServer) Cache(context.Context) (*dagql.SessionCache, error)      { return nil, nil }
func (ms *mockServer) Server(context.Context) (*dagql.Server, error)           { return nil, nil }
func (ms *mockServer) MuxEndpoint(context.Context, string, http.Handler) error { return nil }
func (ms *mockServer) Secrets(context.Context) (*SecretStore, error)           { return nil, nil }
func (ms *mockServer) Sockets(context.Context) (*SocketStore, error)           { return nil, nil }

func (ms *mockServer) AddClientResourcesFromID(ctx context.Context, id *resource.ID, sourceClientID string, skipTopLevel bool) error {
	return nil
}
func (ms *mockServer) Auth(context.Context) (*auth.RegistryAuthProvider, error) { return nil, nil }

func (ms *mockServer) Buildkit(context.Context) (*buildkit.Client, error) { return nil, nil }

func (ms *mockServer) Services(context.Context) (*Services, error) { return nil, nil }

func (ms *mockServer) Platform() Platform               { return Platform{} }
func (ms *mockServer) OCIStore() content.Store          { return nil }
func (ms *mockServer) DNS() *oci.DNSConfig              { return nil }
func (ms *mockServer) LeaseManager() *leaseutil.Manager { return nil }
func (ms *mockServer) EngineLocalCacheEntries(context.Context) (*EngineCacheEntrySet, error) {
	return nil, nil
}

func (ms *mockServer) PruneEngineLocalCacheEntries(context.Context, bool) (*EngineCacheEntrySet, error) {
	return nil, nil
}
func (ms *mockServer) EngineLocalCachePolicy() *bkclient.PruneInfo { return nil }
func (ms *mockServer) BuildkitCache() bkcache.Manager              { return nil }
func (ms *mockServer) BuildkitSession() *bksession.Manager         { return nil }
func (ms *mockServer) Locker() *locker.Locker                      { return nil }
func (ms *mockServer) SecretSalt() []byte                          { return nil }
func (ms *mockServer) FileSyncer() *filesync.FileSyncer            { return nil }
func (ms *mockServer) ClientTelemetry(ctc context.Context, sessID, clientID string) (*clientdb.Queries, func() error, error) {
	return nil, nil, nil
}
func (ms *mockServer) EngineName() string { return "mockEngine" }
func (ms *mockServer) Clients() []string  { return []string{} }

func TestParseCallerCalleeRefs(t *testing.T) {
	mID := call.New().Append(&ast.Type{}, "callee1")
	pcID := call.New().Append(&ast.Type{}, "VersionedGitSSH.hello",
		call.WithModule(call.NewModule(
			mID,
			"versioned_git_ssh",
			"git@github.com:dagger/dagger-test-modules/versioned@main", "0cabe03cc0a9079e738c92b2c589d81fd560011f",
		)))
	_, calleeRef := parseCallerCalleeRefs(t.Context(), &Query{Server: &mockServer{}}, pcID)

	require.Equal(t, "github.com/dagger/dagger-test-modules/versioned", calleeRef.ref)
	require.Equal(t, "0cabe03cc0a9079e738c92b2c589d81fd560011f", calleeRef.version)
	require.Equal(t, "VersionedGitSSH.hello", calleeRef.functionName)
}
