package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/stretchr/testify/require"
)

// versionTestClient is a nested client announcing version, inside a module
// declaring moduleVersion unless that is empty.
type versionTestClient struct {
	version       string
	declared      bool
	moduleVersion string
	headerVersion string
}

// initVersionTestClient registers and initializes tc's client, and returns
// it with the context of its requests.
func initVersionTestClient(t *testing.T, tc versionTestClient) (context.Context, *clientRuntime, *ClientInitOpts) {
	t.Helper()
	srv, sess, parent, ctx, requestScope := newNestedTransportTestFixture(t)
	t.Cleanup(func() { requestScope.Lease().Release() })
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv.engineCache = cache
	srv.clientDBs = clientdb.NewDBs(t.TempDir())
	srv.telemetryPubSub = NewPubSub(srv)
	sess.telemetryPubSub = srv.telemetryPubSub
	sess.engineUtilClient = new(engineutil.Client)
	t.Cleanup(func() { require.NoError(t, srv.clientDBs.Close()) })

	metadata := nestedTransportTestMetadata("client")
	metadata.ClientVersion = tc.version
	metadata.ClientVersionDeclared = tc.declared
	header := http.Header{}
	if tc.headerVersion != "" {
		header = (engine.ClientMetadata{ClientID: metadata.ClientID, ClientVersion: tc.headerVersion}).AppendToHTTPHeaders(header)
		metadata = nestedClientMetadataForRequest(header, metadata)
	}
	transport, err := srv.RegisterNestedClientTransport(ctx, metadata, parent.clientID)
	require.NoError(t, err)
	t.Cleanup(func() { transport.Close() })
	client := sess.clientRuntimes[metadata.ClientID]
	requestMetadata := nestedClientMetadataForRequest(header, metadata)
	require.NoError(t, mergeClientMetadataForTest(sess, client, requestMetadata))
	require.NoError(t, sealClientMetadataForTest(sess, client))
	ctx = engine.ContextWithClientMetadata(ctx, requestMetadata)
	clientScope, err := sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "version test")
	require.NoError(t, err)
	t.Cleanup(func() { clientScope.Lease().Release() })
	ctx, err = engine.ContextWithClientScope(ctx, clientScope)
	require.NoError(t, err)

	opts := &ClientInitOpts{ClientMetadata: requestMetadata, FunctionCall: &core.FunctionCall{}}
	base, err := srv.getCoreSchemaBase(ctx)
	require.NoError(t, err)
	if tc.moduleVersion != "" {
		root := core.NewRoot(srv)
		// a loaded module's deps carry its capped view
		// (defaultSchemaBuilderForEngineVersion)
		view := call.View(engine.ModuleAPIViewVersion(tc.moduleVersion))
		dag, err := base.Fork(ctx, root, view)
		require.NoError(t, err)
		source, err := dagql.NewObjectResultForCall(&core.ModuleSource{EngineVersion: tc.moduleVersion}, dag, &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "versionSource", Type: dagql.NewResultCallType((&core.ModuleSource{}).Type())})
		require.NoError(t, err)
		opts.ModuleContext, err = dagql.NewObjectResultForCall(&core.Module{
			NameField: "version-test", Source: dagql.NonNull(source),
			Deps: core.NewSchemaBuilder(root, []core.Mod{base.CoreMod(view)}),
		}, dag, &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "versionModule", Type: dagql.NewResultCallType((&core.Module{}).Type())})
		require.NoError(t, err)
	}

	require.NoError(t, srv.initializeClientRuntime(ctx, client, opts))
	t.Cleanup(func() {
		require.NoError(t, client.meterProvider.Shutdown(context.Background()))
		require.NoError(t, client.closeTelemetryDB())
	})
	return core.ContextWithQuery(ctx, client.dagqlRoot), client, opts
}

// queryVersionTestClient serves query to client, as serveQuery does, and
// returns the response body.
func queryVersionTestClient(ctx context.Context, t *testing.T, client *clientRuntime, query string) string {
	t.Helper()
	served, err := client.servedMods.Schema(ctx)
	require.NoError(t, err)
	body, err := json.Marshal(map[string]string{"query": query})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewReader(body)).WithContext(client.withModuleNaming(ctx))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	dagql.NewDefaultHandler(served).ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	return resp.Body.String()
}

func TestDeclaredClientVersion(t *testing.T) {
	for _, tc := range []struct {
		name          string
		version       string
		declared      bool
		moduleVersion string
		headerVersion string
		want          string
	}{
		{name: "declared module client", version: "v0.21.6", declared: true, moduleVersion: engine.Version, want: "v0.21.6"},
		{name: "nested CLI keeps its version", version: engine.Version, declared: true, headerVersion: "v0.21.6", moduleVersion: engine.Version, want: "v0.21.6"},
		{name: "nested CLI declares without exec variable", version: engine.Version, headerVersion: "v0.21.6", moduleVersion: engine.Version, want: "v0.21.6"},
		{name: "nested CLI engine cap", version: "v0.21.6", declared: true, headerVersion: "v99.0.0", moduleVersion: "v0.21.6", want: engine.APIViewVersion(engine.Version)},
		{name: "nested CLI explicit engine cap", version: "v2.0.0", headerVersion: "v2.0.0", moduleVersion: "v0.21.6", want: engine.APIViewVersion(engine.Version)},
		{name: "nested CLI explicit dev engine cap", version: "v2.0.0-dev-123", headerVersion: "v2.0.0-dev-123", moduleVersion: "v0.21.6", want: engine.APIViewVersion(engine.Version)},
		{name: "undeclared module client", version: "v0.21.6", moduleVersion: engine.Version, want: engine.APIViewVersion(engine.Version)},
		{name: "undeclared module client past engine", version: "v0.21.6", moduleVersion: "v1.0.0", want: engine.ModuleAPIViewVersion("v1.0.0")},
		{name: "legacy module", version: engine.Version, moduleVersion: "v0.21.6", want: "v0.21.6"},
		{name: "cap at engine not module", version: "v99.0.0", declared: true, moduleVersion: "v0.21.6", want: engine.APIViewVersion(engine.Version)},
		// v1.0.0 prereleases before the cut share one view
		{name: "beta view", version: "v1.0.0-beta.18", declared: true, moduleVersion: engine.Version, want: engine.APIViewVersion("v1.0.0-beta.18")},
		// a declared version past the engine's view is capped at it, like a
		// module's engineVersion (ModuleAPIViewVersion)
		{name: "declared release past prerelease engine", version: "v1.0.0", declared: true, moduleVersion: engine.Version, want: engine.APIViewVersion(engine.Version)},
		{name: "unprefixed version", version: "0.21.6", declared: true, moduleVersion: engine.Version, want: "v0.21.6"},
		{name: "declared container client", version: "v0.21.6", declared: true, want: "v0.21.6"},
		{name: "declared container remains uncapped", version: "v99.0.0", declared: true, want: "v99.0.0"},
		{name: "standalone explicit", version: "v2.0.0", headerVersion: "v2.0.0", want: "v2.0.0"},
		{name: "standalone explicit dev", version: "v2.0.0-dev-123", headerVersion: "v2.0.0-dev-123", want: "v2.0.0"},
		{name: "standalone headerless explicit", version: "v2.0.0", want: "v2.0.0"},
		{name: "standalone headerless explicit dev", version: "v2.0.0-dev-123", want: "v2.0.0"},
		{name: "undeclared version remains uncapped", version: "v99.0.0", want: "v99.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, client, opts := initVersionTestClient(t, versionTestClient{
				version:       tc.version,
				declared:      tc.declared,
				moduleVersion: tc.moduleVersion,
				headerVersion: tc.headerVersion,
			})
			require.Equal(t, call.View(tc.want), client.dag.View)
			require.Equal(t, opts.ClientMetadata.ClientVersion, client.clientVersion)
			require.Equal(t, tc.declared || tc.headerVersion != "", client.clientMetadata.ClientVersionDeclared)
			require.Same(t, opts.FunctionCall, client.fnCall)
			require.Equal(t, tc.moduleVersion != "", client.mod.Self() != nil)
			if tc.moduleVersion != "" {
				require.Same(t, opts.ModuleContext.Self(), client.mod.Self())
				originalCore, ok := client.mod.Self().Deps.Lookup(core.ModuleName)
				require.True(t, ok)
				originalView, _ := originalCore.View()
				require.Equal(t, call.View(engine.ModuleAPIViewVersion(tc.moduleVersion)), originalView)
			}
			served, err := client.servedMods.Schema(ctx)
			require.NoError(t, err)
			require.Equal(t, call.View(tc.want), served.View)
			require.JSONEq(t, `{"data":{"__schemaVersion":"`+tc.want+`"}}`, queryVersionTestClient(ctx, t, client, "{ __schemaVersion }"))
			_, reevaluate := served.Schema().Directives["reevaluate"]
			require.Equal(t, tc.want != "v0.21.6", reevaluate)
		})
	}
}

// A declared client version moves only the core API a module's code sees: the
// typedefs its runtime registers are named by the module's own rules (its
// NamingView), on either side of the identifier casing cut.
func TestDeclaredModuleClientNaming(t *testing.T) {
	const preCut = "v1.0.0-beta.20"
	cut := engine.IdentifierCasingVersion
	for _, tc := range []struct {
		name     string
		client   versionTestClient
		coreView string
		typeName string
	}{
		{
			name:     "pre-cut module, post-cut client",
			client:   versionTestClient{version: cut, declared: true, moduleVersion: preCut},
			coreView: cut,
			typeName: "Httpclient",
		},
		{
			name:     "post-cut module, pre-cut client",
			client:   versionTestClient{version: "v0.21.6", declared: true, moduleVersion: cut},
			coreView: "v0.21.6",
			typeName: "HTTPClient",
		},
		{
			name:     "pre-cut module, undeclared client",
			client:   versionTestClient{version: cut, moduleVersion: preCut},
			coreView: engine.APIViewVersion(preCut),
			typeName: "Httpclient",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, client, _ := initVersionTestClient(t, tc.client)
			require.Equal(t, call.View(tc.coreView), client.dag.View)
			require.JSONEq(t, `{"data":{"__schemaVersion":"`+tc.coreView+`"}}`, queryVersionTestClient(ctx, t, client, "{ __schemaVersion }"))
			require.JSONEq(t,
				`{"data":{"typeDef":{"withObject":{"asObject":{"name":"`+tc.typeName+`"}}}}}`,
				queryVersionTestClient(ctx, t, client, `{ typeDef { withObject(name: "HTTPClient") { asObject { name } } } }`))
		})
	}
}

func TestNestedClientDeclaredVersionMetadata(t *testing.T) {
	for _, version := range []string{"v0.21.6", "v1.0.0-beta.18", ""} {
		t.Run(version, func(t *testing.T) {
			srv, sess, parent, ctx, scope := newNestedTransportTestFixture(t)
			defer scope.Lease().Release()
			base := nestedTransportTestMetadata("nested")
			base.ClientVersion = "v1.0.0-beta.22"
			base.ClientVersionDeclared = true
			base.SDKEntrypoint = true
			header := (engine.ClientMetadata{ClientID: "nested", ClientVersion: version, SessionID: "untrusted", ClientSecretToken: "untrusted"}).AppendToHTTPHeaders(http.Header{})
			metadata := nestedClientMetadataForRequest(header, base)
			want := version
			if want == "" {
				want = base.ClientVersion
			}
			require.Equal(t, want, metadata.ClientVersion)
			require.Equal(t, base.SessionID, metadata.SessionID)
			require.Equal(t, base.ClientSecretToken, metadata.ClientSecretToken)
			transport, err := srv.RegisterNestedClientTransport(ctx, metadata, parent.clientID)
			require.NoError(t, err)
			defer transport.Close()
			client := sess.clientRuntimes[metadata.ClientID]
			require.NoError(t, mergeClientMetadataForTest(sess, client, nestedClientMetadataForRequest(header, metadata)))
			require.NoError(t, sealClientMetadataForTest(sess, client))
			require.True(t, client.clientMetadata.ClientVersionDeclared)
			require.True(t, client.clientMetadata.SDKEntrypoint)
			require.Equal(t, want, client.clientVersion)
			conflicting := *metadata
			conflicting.ClientVersion = "v0.20.0"
			require.ErrorContains(t, mergeClientMetadataForTest(sess, client, &conflicting), "conflicts with bootstrap value")
			wire, err := engine.ClientMetadataFromHTTPHeaders(metadata.AppendToHTTPHeaders(http.Header{}))
			require.NoError(t, err)
			require.False(t, wire.ClientVersionDeclared)
		})
	}
}
