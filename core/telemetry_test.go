package core

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	engineclient "github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/clientdb"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	telemetry "github.com/dagger/otel-go"
	"github.com/moby/locker"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

func testResultCall(field string, typ dagql.Typed, receiver *dagql.ResultCall) *dagql.ResultCall {
	var ref *dagql.ResultCallRef
	if receiver != nil {
		ref = &dagql.ResultCallRef{Call: receiver}
	}
	return &dagql.ResultCall{
		Kind:     dagql.ResultCallKindField,
		Field:    field,
		Type:     dagql.NewResultCallType(typ.Type()),
		Receiver: ref,
	}
}

type mockServer struct {
	moduleSource   *ModuleSource
	functionCall   *FunctionCall
	clientMetadata *engine.ClientMetadata
}

func (ms *mockServer) ServeHTTPToNestedClient(http.ResponseWriter, *http.Request, *buildkit.ExecutionMetadata) {
}

func (ms *mockServer) ServeModule(ctx context.Context, mod dagql.ObjectResult[*Module], includeDependencies bool) error {
	return nil
}

func (ms *mockServer) CurrentModule(ctx context.Context) (dagql.ObjectResult[*Module], error) {
	var zero dagql.ObjectResult[*Module]
	if ms.moduleSource == nil {
		return zero, nil
	}
	cacheIface, err := dagql.NewCache(context.Background(), "", nil)
	if err != nil {
		panic(err)
	}
	ctx = dagql.ContextWithCache(ctx, cacheIface)
	dag := dagql.NewServer(&Query{})
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*ModuleSource]{Typed: &ModuleSource{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Module]{Typed: &Module{}}))

	sourceRes, err := dagql.NewObjectResultForCall(ms.moduleSource, dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "mock_module_source",
		Type:        dagql.NewResultCallType(ms.moduleSource.Type()),
	})
	if err != nil {
		panic(err)
	}

	dn := dagql.Nullable[dagql.ObjectResult[*ModuleSource]]{
		Valid: true,
		Value: sourceRes,
	}
	return dagql.NewObjectResultForCall(&Module{
		Source: dn,
	}, dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "mock_current_module",
		Type:        dagql.NewResultCallType((&Module{}).Type()),
	})
}

func (ms *mockServer) ModuleParent(context.Context) (dagql.ObjectResult[*Module], error) {
	return dagql.ObjectResult[*Module]{}, nil
}

func (ms *mockServer) CurrentFunctionCall(context.Context) (*FunctionCall, error) {
	return ms.functionCall, nil
}

func (ms *mockServer) CurrentServedDeps(context.Context) (*ModDeps, error) {
	return &ModDeps{}, nil
}

func (ms *mockServer) MainClientCallerMetadata(context.Context) (*engine.ClientMetadata, error) {
	if ms.clientMetadata != nil {
		return ms.clientMetadata, nil
	}
	return &engine.ClientMetadata{}, nil
}

func (ms *mockServer) SpecificClientMetadata(context.Context, string) (*engine.ClientMetadata, error) {
	return nil, nil
}

func (ms *mockServer) SpecificClientAttachableConn(context.Context, string) (*grpc.ClientConn, error) {
	return nil, nil
}

func (ms *mockServer) NonModuleParentClientMetadata(context.Context) (*engine.ClientMetadata, error) {
	return nil, nil
}
func (ms *mockServer) DefaultDeps(context.Context) (*ModDeps, error) { return nil, nil }
func (ms *mockServer) Cache(context.Context) (*dagql.Cache, error)   { return nil, nil }
func (ms *mockServer) TelemetrySeenKeyStore(context.Context) (dagql.TelemetrySeenKeyStore, error) {
	return nil, nil
}
func (ms *mockServer) Server(context.Context) (*dagql.Server, error)           { return nil, nil }
func (ms *mockServer) MuxEndpoint(context.Context, string, http.Handler) error { return nil }

func (ms *mockServer) Auth(context.Context) (*auth.RegistryAuthProvider, error) { return nil, nil }

func (ms *mockServer) Buildkit(context.Context) (*buildkit.Client, error) { return nil, nil }

func (ms *mockServer) RegistryResolver(context.Context) (*serverresolver.Resolver, error) {
	return nil, nil
}

func (ms *mockServer) Services(context.Context) (*Services, error) { return nil, nil }

func (ms *mockServer) Platform() Platform               { return Platform{} }
func (ms *mockServer) OCIStore() content.Store          { return nil }
func (ms *mockServer) BuiltinOCIStore() content.Store   { return nil }
func (ms *mockServer) DNS() *oci.DNSConfig              { return nil }
func (ms *mockServer) LeaseManager() *leaseutil.Manager { return nil }
func (ms *mockServer) EngineLocalCacheEntries(context.Context) (*EngineCacheEntrySet, error) {
	return nil, nil
}

func (ms *mockServer) PruneEngineLocalCacheEntries(context.Context, EngineCachePruneOptions) (*EngineCacheEntrySet, error) {
	return nil, nil
}
func (ms *mockServer) EngineLocalCachePolicy() *dagql.CachePrunePolicy { return nil }
func (ms *mockServer) SnapshotManager() bkcache.SnapshotManager        { return nil }
func (ms *mockServer) Locker() *locker.Locker                          { return nil }
func (ms *mockServer) SecretSalt() []byte                              { return nil }
func (ms *mockServer) ClientTelemetry(ctc context.Context, sessID, clientID string) (*clientdb.DB, error) {
	return nil, nil
}
func (ms *mockServer) EngineName() string { return "mockEngine" }
func (ms *mockServer) Clients() []string  { return []string{} }

func (ms *mockServer) CloudEngineClient(context.Context, string, string, []string) (*engineclient.Client, bool, error) {
	return nil, false, nil
}

func (ms *mockServer) CleanMountNS() *os.File { return nil }

func TestParseCallerCalleeRefs(t *testing.T) {
	call := &dagql.ResultCall{
		Kind:  dagql.ResultCallKindField,
		Field: "VersionedGitSSH.hello",
		Type:  dagql.NewResultCallType((&Void{}).Type()),
		Module: &dagql.ResultCallModule{
			Name: "versioned_git_ssh",
			Ref:  "git@github.com:dagger/dagger-test-modules/versioned@main",
			Pin:  "0cabe03cc0a9079e738c92b2c589d81fd560011f",
		},
	}

	// Set up mock server with Git source for the caller
	mockSrv := &mockServer{
		moduleSource: &ModuleSource{
			Kind: ModuleSourceKindGit,
			Git: &GitModuleSource{
				CloneRef: "git@github.com:dagger/dagger-test-modules/caller",
				Version:  "v1.0.0",
			},
		},
		functionCall: &FunctionCall{
			Name: "callerFunction",
		},
	}

	callerRef, calleeRef := parseCallerCalleeRefs(t.Context(), &Query{Server: mockSrv}, call)

	require.NotNil(t, callerRef)
	require.Equal(t, "github.com/dagger/dagger-test-modules/caller", callerRef.ref)
	require.Equal(t, "v1.0.0", callerRef.version)
	require.Equal(t, "callerFunction", callerRef.functionName)

	require.NotNil(t, calleeRef)
	require.Equal(t, "github.com/dagger/dagger-test-modules/versioned", calleeRef.ref)
	require.Equal(t, "0cabe03cc0a9079e738c92b2c589d81fd560011f", calleeRef.version)
	require.Equal(t, "VersionedGitSSH.hello", calleeRef.functionName)
}

func TestAroundFuncMarksIntrospectionRootAsSkipped(t *testing.T) {
	req := &dagql.CallRequest{
		ResultCall: testResultCall("currentTypeDefs", dagql.String(""), nil),
	}

	ctx, _ := AroundFunc(t.Context(), req)
	require.True(t, dagql.IsSkipped(ctx))
}

func TestAroundFuncSkipsIntrospectionDescendantsViaContext(t *testing.T) {
	rootReq := &dagql.CallRequest{
		ResultCall: testResultCall("currentTypeDefs", dagql.String(""), nil),
	}
	rootCtx, _ := AroundFunc(t.Context(), rootReq)
	require.True(t, dagql.IsSkipped(rootCtx))

	childReq := &dagql.CallRequest{
		ResultCall: testResultCall(
			"name",
			dagql.String(""),
			rootReq.ResultCall,
		),
	}
	childCtx, _ := AroundFunc(rootCtx, childReq)
	require.True(t, dagql.IsSkipped(childCtx))
}

func TestIsIntrospectionPreservesClassification(t *testing.T) {
	cache, err := dagql.NewCache(t.Context(), "", nil)
	require.NoError(t, err)
	ctx := dagql.ContextWithCache(t.Context(), cache)

	functionFrame := testResultCall("function", dagql.String(""), nil)
	functionFrame.Type = dagql.NewResultCallType((&Function{}).Type())

	tests := []struct {
		name  string
		frame *dagql.ResultCall
		want  bool
	}{
		{
			name:  "root currentTypeDefs",
			frame: testResultCall("currentTypeDefs", dagql.String(""), nil),
			want:  true,
		},
		{
			name:  "root plain field",
			frame: testResultCall("plain", dagql.String(""), nil),
			want:  false,
		},
		{
			name:  "function builder field",
			frame: testResultCall("withArg", dagql.String(""), functionFrame),
			want:  true,
		},
		{
			name:  "descendant of introspection root",
			frame: testResultCall("name", dagql.String(""), testResultCall("currentTypeDefs", dagql.String(""), nil)),
			want:  true,
		},
		{
			name:  "descendant of plain root",
			frame: testResultCall("name", dagql.String(""), testResultCall("plain", dagql.String(""), nil)),
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isIntrospection(ctx, tc.frame))
		})
	}
}

type telemetryTestNoopResolver struct{}

func (telemetryTestNoopResolver) ObjectType(string) (dagql.ObjectType, bool) { return nil, false }
func (telemetryTestNoopResolver) ScalarType(string) (dagql.ScalarType, bool) { return nil, false }

type telemetryTestSpan struct {
	trace.Span
	attrs []attribute.KeyValue
}

func (s *telemetryTestSpan) End(...trace.SpanEndOption)              {}
func (s *telemetryTestSpan) AddEvent(string, ...trace.EventOption)   {}
func (s *telemetryTestSpan) AddLink(trace.Link)                      {}
func (s *telemetryTestSpan) IsRecording() bool                       { return true }
func (s *telemetryTestSpan) RecordError(error, ...trace.EventOption) {}
func (s *telemetryTestSpan) SpanContext() trace.SpanContext          { return trace.SpanContext{} }
func (s *telemetryTestSpan) SetStatus(codes.Code, string)            {}
func (s *telemetryTestSpan) SetName(string)                          {}
func (s *telemetryTestSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.attrs = append(s.attrs, attrs...)
}
func (s *telemetryTestSpan) TracerProvider() trace.TracerProvider {
	return trace.NewNoopTracerProvider()
}

type telemetryTestLazyString struct {
	dagql.String
}

func (telemetryTestLazyString) Type() *ast.Type {
	return dagql.String("").Type()
}

func (telemetryTestLazyString) LazyEvalFunc() dagql.LazyEvalFunc {
	return func(context.Context) error { return nil }
}

func TestRecordStatusDoesNotMarkPendingLazyResultCached(t *testing.T) {
	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "", nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cacheIface)

	reqCall := &dagql.ResultCall{
		Kind:  dagql.ResultCallKindField,
		Field: "withExec",
		Type:  dagql.NewResultCallType(dagql.String("").Type()),
	}
	req := &dagql.CallRequest{ResultCall: reqCall}

	res, err := cacheIface.GetOrInitCall(ctx, "test-session", telemetryTestNoopResolver{}, req, func(ctx context.Context) (dagql.AnyResult, error) {
		return dagql.NewResultForCurrentCall(ctx, telemetryTestLazyString{String: dagql.String("lazy")})
	})
	require.NoError(t, err)
	require.True(t, dagql.HasPendingLazyEvaluation(res))

	span := &telemetryTestSpan{}
	recordStatus(ctx, res, span, true, reqCall)

	for _, attr := range span.attrs {
		require.NotEqual(t, telemetry.CachedAttr, string(attr.Key))
	}
}
