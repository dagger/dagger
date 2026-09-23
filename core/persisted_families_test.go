package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// Shared fixtures for the persisted family tests: a path-backed cache with the
// test snapshot manager, a dagql server with every class the fixtures decode,
// and helpers to attach persistable rows, restart the cache and compare the
// declared references of a payload with the rows its attachment hook owns.

type persistedFamiliesTestQueryServer struct {
	*cacheVolumeTestQueryServer
	root *Query
	dag  *dagql.Server
}

// Server returns the fixture's dagql server, the way a served session does.
// Values that resolve a stored handle need it to load the row the handle
// names (core/object.go attachModuleObjectHandleValue).
func (s *persistedFamiliesTestQueryServer) Server(context.Context) (*dagql.Server, error) {
	return s.dag, nil
}

// DefaultDeps returns an empty dependency set so module rows can be decoded
// and served without a live module runtime.
func (s *persistedFamiliesTestQueryServer) DefaultDeps(context.Context) (*SchemaBuilder, error) {
	return NewSchemaBuilder(s.root, nil), nil
}

type persistedFamiliesTestEnv struct {
	dbPath  string
	manager *containerPersistenceTestSnapshots
	session string
	// attachables are the in-process client connections a restored session
	// resource resolves through, keyed by client ID. They stand in for the
	// clients that would supply fresh secret and socket material.
	attachables map[string]*grpc.ClientConn
}

func newPersistedFamiliesTestEnv(t *testing.T, session string) *persistedFamiliesTestEnv {
	t.Helper()
	return &persistedFamiliesTestEnv{
		dbPath:  filepath.Join(t.TempDir(), "cache.db"),
		manager: newContainerPersistenceTestSnapshots(),
		session: session,
	}
}

// open starts (or restarts) the cache on the environment's database and
// returns a context carrying the query, cache and client metadata.
func (env *persistedFamiliesTestEnv) open(t *testing.T) (context.Context, *dagql.Cache, *dagql.Server) {
	t.Helper()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: env.session, SessionID: env.session})
	cache, err := dagql.NewCache(ctx, env.dbPath, env.manager, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	require.Equal(t, dagql.CachePersistenceResetNone, cache.PersistenceResetReason(), "the saved store reopens cleanly")
	qs := &persistedFamiliesTestQueryServer{cacheVolumeTestQueryServer: &cacheVolumeTestQueryServer{mockServer: &mockServer{attachables: env.attachables}, cacheManager: env.manager}}
	query := &Query{Server: qs}
	qs.root = query
	ctx = ContextWithQuery(dagql.ContextWithCache(ctx, cache), query)
	srv := newCoreDagqlServerForTest(t, query)
	qs.dag = srv
	for _, class := range []dagql.ObjectType{
		dagql.NewClass(srv, dagql.ClassOpts[*Container]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*File]{}),
		dagql.NewClass(srv, dagql.ClassOpts[EnvVariable]{}),
		dagql.NewClass(srv, dagql.ClassOpts[Port]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*SDKConfig]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*GitBundleRef]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*Schema]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*CurrentModule]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*WorkspaceMigration]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*WorkspaceMigrationStep]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*Cloud]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*TerminalLegacy]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*modules.ModuleConfigClient]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*Changeset]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*Module]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*ModuleSource]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*Error]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*Workspace]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*Check]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*CheckGroup]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*Up]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*UpGroup]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*TerminalTarget]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*TerminalGroup]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*Secret]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*Socket]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*Service]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*RemoteGitMirror]{}),
		dagql.NewClass(srv, dagql.ClassOpts[*GitRepository]{}),
	} {
		srv.InstallObject(class)
	}
	srv.InstallScalar(Void{})
	installTypeDefTestClasses(srv)
	return ctx, cache, srv
}

// restart saves the cache and reopens it on the same database.
func (env *persistedFamiliesTestEnv) restart(t *testing.T, ctx context.Context, cache *dagql.Cache) (context.Context, *dagql.Cache, *dagql.Server) {
	t.Helper()
	require.NoError(t, cache.ReleaseSession(ctx, env.session))
	require.NoError(t, cache.Close(ctx))
	return env.open(t)
}

// attach retains one persistable row for a value whose class is installed.
func (env *persistedFamiliesTestEnv) attach(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, field string, self dagql.Typed) dagql.AnyResult {
	t.Helper()
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType(self.Type())}
	res, err := cache.GetOrInitCall(ctx, env.session, srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		objType, ok := srv.ObjectType(self.Type().Name())
		if !ok {
			return nil, fmt.Errorf("no installed class for %q", self.Type().Name())
		}
		valRes, err := dagql.NewResultForCall(self, frame)
		if err != nil {
			return nil, err
		}
		return objType.New(valRes)
	})
	require.NoError(t, err)
	return res
}

// attachWithResourceHandle retains a row that a session must bind a resource
// for before it may use it, the way host secrets and sockets are created.
// The handle is set before attachment, since an attached row's handle cannot
// change.
func (env *persistedFamiliesTestEnv) attachWithResourceHandle(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, field string, self dagql.Typed, handle dagql.SessionResourceHandle) dagql.AnyResult {
	t.Helper()
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType(self.Type())}
	res, err := cache.GetOrInitCall(ctx, env.session, srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(ctx context.Context) (dagql.AnyResult, error) {
		objType, ok := srv.ObjectType(self.Type().Name())
		if !ok {
			return nil, fmt.Errorf("no installed class for %q", self.Type().Name())
		}
		valRes, err := dagql.NewResultForCall(self, frame)
		if err != nil {
			return nil, err
		}
		obj, err := objType.New(valRes)
		if err != nil {
			return nil, err
		}
		return obj.WithSessionResourceHandleAny(ctx, handle)
	})
	require.NoError(t, err)
	return res
}

func (env *persistedFamiliesTestEnv) directory(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, field, snapshotID string) dagql.ObjectResult[*Directory] {
	t.Helper()
	res := attachStoredSnapshotTestValue(t, ctx, cache, srv, env.session, field, storedSnapshotTestValue("Directory", snapshotID, "/", false), true)
	dir, ok := res.(dagql.ObjectResult[*Directory])
	require.True(t, ok)
	return dir
}

func persistedRowID(t *testing.T, cache *dagql.Cache, res dagql.AnyResult) uint64 {
	t.Helper()
	id, err := cache.PersistedResultID(res)
	require.NoError(t, err)
	require.NotZero(t, id)
	return id
}

func persistedEncoding(t *testing.T, ctx context.Context, cache *dagql.Cache, res dagql.AnyResult) dagql.PersistedResultEncoding {
	t.Helper()
	encoding, err := dagql.DefaultPersistedSelfCodec.EncodeResult(ctx, cache, res)
	require.NoError(t, err)
	return encoding
}

func persistedEncodingBytes(t *testing.T, ctx context.Context, cache *dagql.Cache, res dagql.AnyResult) []byte {
	t.Helper()
	data, err := json.Marshal(persistedEncoding(t, ctx, cache, res).Envelope)
	require.NoError(t, err)
	return data
}

// persistedVisitedRefs runs a row's family visitor and returns the declared
// child row references it reports, keyed by their declared path.
func persistedVisitedRefs(t *testing.T, ctx context.Context, cache *dagql.Cache, res dagql.AnyResult) map[string]uint64 {
	t.Helper()
	encoding := persistedEncoding(t, ctx, cache, res)
	require.Equal(t, "object_self", encoding.Envelope.Kind)
	family, ok := dagql.PersistedObjectFamilyByName(encoding.Envelope.ObjectCodec)
	require.True(t, ok, "family %q is registered", encoding.Envelope.ObjectCodec)
	frame, err := res.ResultCall()
	require.NoError(t, err)
	refs := map[string]uint64{}
	_, err = family.Visitor.VisitPersistedReferences(dagql.PersistedPayloadVisit{
		Version:       encoding.Envelope.Version,
		Call:          frame,
		Path:          dagql.PersistedRefPath{}.Field("objectJSON"),
		Payload:       encoding.Envelope.ObjectJSON,
		SnapshotLinks: encoding.SnapshotLinks,
	}, func(ref *dagql.PersistedRef) error {
		if ref.Kind == dagql.PersistedRefChild {
			_, dup := refs[ref.Path.String()]
			require.False(t, dup, "path %s reported twice", ref.Path)
			refs[ref.Path.String()] = ref.ResultID
		}
		return nil
	})
	require.NoError(t, err)
	return refs
}

// assertPersistedRefsMatchOwnership checks that the rows a family's visitor
// declares are exactly the rows its dependency hook owns, apart from rows the
// decoding server supplies rather than the payload (HasDecodedDependencyResults),
// which the decoded row owns without declaring them in the envelope.
func assertPersistedRefsMatchOwnership(t *testing.T, ctx context.Context, cache *dagql.Cache, res dagql.AnyResult) map[string]uint64 {
	t.Helper()
	refs := persistedVisitedRefs(t, ctx, cache, res)
	declared := make([]uint64, 0, len(refs))
	for _, id := range refs {
		declared = append(declared, id)
	}
	if decoded, ok := res.Unwrap().(dagql.HasDecodedDependencyResults); ok {
		for _, dep := range decoded.DecodedDependencyResults() {
			declared = append(declared, persistedRowID(t, cache, dep))
		}
	}
	slices.Sort(declared)
	declared = slices.Compact(declared)

	hook, ok := res.Unwrap().(dagql.HasDependencyResults)
	require.True(t, ok, "%T declares references and must own them", res.Unwrap())
	ownedResults, err := hook.AttachDependencyResults(ctx, res, func(dep dagql.AnyResult) (dagql.AnyResult, error) { return dep, nil })
	require.NoError(t, err)
	owned := make([]uint64, 0, len(ownedResults))
	for _, dep := range ownedResults {
		owned = append(owned, persistedRowID(t, cache, dep))
	}
	slices.Sort(owned)
	owned = slices.Compact(owned)
	require.Equal(t, owned, declared, "declared references must be exactly the owned rows")
	return refs
}
