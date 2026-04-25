package schema

import (
	"context"
	"net/http"
	"os"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	engineclient "github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/engineutil"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/moby/locker"
	"google.golang.org/grpc"
)

func (s *currentTypeDefsTestServer) ServeModule(context.Context, dagql.ObjectResult[*core.Module], bool, bool) error {
	return nil
}

func (s *currentTypeDefsTestServer) CurrentModule(context.Context) (dagql.ObjectResult[*core.Module], error) {
	return dagql.ObjectResult[*core.Module]{}, nil
}

func (s *currentTypeDefsTestServer) ModuleParent(context.Context) (dagql.ObjectResult[*core.Module], error) {
	return dagql.ObjectResult[*core.Module]{}, nil
}

func (s *currentTypeDefsTestServer) CurrentFunctionCall(context.Context) (*core.FunctionCall, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) CurrentEnv(context.Context) (*call.ID, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) CurrentWorkspace(context.Context) (*core.Workspace, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) CurrentServedDeps(context.Context) (*core.SchemaBuilder, error) {
	return s.deps, nil
}

func (s *currentTypeDefsTestServer) MainClientCallerMetadata(context.Context) (*engine.ClientMetadata, error) {
	return &engine.ClientMetadata{}, nil
}

func (s *currentTypeDefsTestServer) NonModuleParentClientMetadata(context.Context) (*engine.ClientMetadata, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) SpecificClientMetadata(context.Context, string) (*engine.ClientMetadata, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) SpecificClientAttachableConn(context.Context, string) (*grpc.ClientConn, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) DefaultDeps(context.Context) (*core.SchemaBuilder, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) TelemetrySeenKeyStore(context.Context) (dagql.TelemetrySeenKeyStore, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) Server(context.Context) (*dagql.Server, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) MuxEndpoint(context.Context, string, http.Handler) error {
	return nil
}

func (s *currentTypeDefsTestServer) ServeHTTPToNestedClient(http.ResponseWriter, *http.Request, *engineutil.ExecutionMetadata) {
}

func (s *currentTypeDefsTestServer) Auth(context.Context) (*auth.RegistryAuthProvider, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) Engine(context.Context) (*engineutil.Client, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) RegistryResolver(context.Context) (*serverresolver.Resolver, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) Services(context.Context) (*core.Services, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) Platform() core.Platform { return core.Platform{} }

func (s *currentTypeDefsTestServer) OCIStore() content.Store { return nil }

func (s *currentTypeDefsTestServer) BuiltinOCIStore() content.Store { return nil }

func (s *currentTypeDefsTestServer) DNS() *oci.DNSConfig { return nil }

func (s *currentTypeDefsTestServer) LeaseManager() *leaseutil.Manager { return nil }

func (s *currentTypeDefsTestServer) EngineLocalCacheEntries(context.Context) (*core.EngineCacheEntrySet, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) PruneEngineLocalCacheEntries(context.Context, core.EngineCachePruneOptions) (*core.EngineCacheEntrySet, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) RegisterSSHFSVolume(context.Context, string, *core.Secret, *core.Secret) (*core.Volume, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) EngineLocalCachePolicy() *dagql.CachePrunePolicy { return nil }

func (s *currentTypeDefsTestServer) SnapshotManager() bkcache.SnapshotManager { return nil }

func (s *currentTypeDefsTestServer) Locker() *locker.Locker { return nil }

func (s *currentTypeDefsTestServer) SecretSalt() []byte { return nil }

func (s *currentTypeDefsTestServer) FlushSessionTelemetry(context.Context) error {
	return nil
}

func (s *currentTypeDefsTestServer) ClientTelemetry(context.Context, string, string) (*clientdb.DB, error) {
	return nil, nil
}

func (s *currentTypeDefsTestServer) EngineName() string { return "testEngine" }

func (s *currentTypeDefsTestServer) Clients() []string { return nil }

func (s *currentTypeDefsTestServer) CloudEngineClient(context.Context, string, string, []string) (*engineclient.Client, bool, error) {
	return nil, false, nil
}

func (s *currentTypeDefsTestServer) CleanMountNS() *os.File { return nil }
