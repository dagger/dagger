package schema

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

// transferTestServer is the core query's server with a snapshot store and a
// given secret salt, as an engine run with DAGGER_SECRET_SALT has.
type transferTestServer struct {
	*llmAuthSchemaServer
	manager bkcache.SnapshotManager
	salt    []byte
}

func (s *transferTestServer) SnapshotManager() bkcache.SnapshotManager { return s.manager }

func (s *transferTestServer) SecretSalt() []byte { return s.salt }

// transferTestEngine is one engine: its cache and its core schema. Each of
// its clients has its own secrets service.
type transferTestEngine struct {
	cache *dagql.Cache
	dag   *dagql.Server
	srv   *transferTestServer
	base  context.Context
}

func newTransferTestEngine(t *testing.T, salt []byte) *transferTestEngine {
	t.Helper()
	store := testutil.NewStore(t)
	main := &engine.ClientMetadata{ClientID: "main-client", SessionID: "main-session"}
	srv := &transferTestServer{
		llmAuthSchemaServer: &llmAuthSchemaServer{
			currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: main, platform: core.Platform{OS: "linux", Architecture: "amd64"}},
			parent:                    main,
			attachables:               map[string]*grpc.ClientConn{},
		},
		manager: store.Manager,
		salt:    salt,
	}
	ctx := engine.ContextWithClientMetadata(t.Context(), main)
	cache, err := dagql.NewCache(ctx, "", store.Manager, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	root := core.NewRoot(srv)
	ctx = core.ContextWithQuery(ctx, root)
	base, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	srv.deps = core.NewSchemaBuilder(root, []core.Mod{base.CoreMod("")})
	dag, err := base.Fork(ctx, root, "")
	require.NoError(t, err)
	srv.dag = dag
	return &transferTestEngine{cache: cache, dag: dag, srv: srv, base: ctx}
}

// session returns a new client and session of the engine, whose secrets
// service resolves env://TRANSFER_SECRET.
func (e *transferTestEngine) session(t *testing.T, name string) context.Context {
	t.Helper()
	md := &engine.ClientMetadata{ClientID: name + "-client", SessionID: name + "-session"}
	e.srv.attachables[md.ClientID] = newLLMAuthAttachable(t, &llmAuthSecrets{vars: map[string]string{"env://TRANSFER_SECRET": "transfer-plaintext"}})
	return engine.ContextWithClientMetadata(e.base, md)
}

func (e *transferTestEngine) export(t *testing.T, ctx context.Context, root dagql.AnyResult) dagql.ValueBundle {
	t.Helper()
	var bundle dagql.ValueBundle
	require.NoError(t, e.cache.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{root}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		bundle = exported.Bundle
		return nil
	}))
	return bundle
}

// transferTestBuild selects a result in ctx's session. The variant is what
// changes between the two calls.
type transferTestBuild func(t *testing.T, e *transferTestEngine, ctx context.Context, variant string) dagql.AnyResult

// withEnv is container().withEnvVariable("S", variant).
func withEnv(t *testing.T, e *transferTestEngine, ctx context.Context, variant string) dagql.AnyResult {
	t.Helper()
	var ctr dagql.ObjectResult[*core.Container]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &ctr,
		dagql.Selector{Field: "container"},
		dagql.Selector{Field: "withEnvVariable", Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString("S")},
			{Name: "value", Value: dagql.NewString(variant)},
		}}))
	return ctr
}

// withSecret is container().withSecretVariable("S", <the secret selected>).
func withSecret(selectors ...dagql.Selector) transferTestBuild {
	return func(t *testing.T, e *transferTestEngine, ctx context.Context, _ string) dagql.AnyResult {
		t.Helper()
		var secret dagql.ObjectResult[*core.Secret]
		require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &secret, selectors...))
		secretID, err := secret.ID()
		require.NoError(t, err)
		var ctr dagql.ObjectResult[*core.Container]
		require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &ctr,
			dagql.Selector{Field: "container"},
			dagql.Selector{Field: "withSecretVariable", Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.NewString("S")},
				{Name: "secret", Value: dagql.NewID[*core.Secret](secretID)},
			}}))
		return ctr
	}
}

// withHostSocket is container().withUnixSocket("/run/transfer.sock",
// host().unixSocket("/tmp/transfer.sock")).
func withHostSocket(t *testing.T, e *transferTestEngine, ctx context.Context, _ string) dagql.AnyResult {
	t.Helper()
	var socket dagql.ObjectResult[*core.Socket]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &socket,
		dagql.Selector{Field: "host"},
		dagql.Selector{Field: "unixSocket", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("/tmp/transfer.sock")},
		}}))
	socketID, err := socket.ID()
	require.NoError(t, err)
	var ctr dagql.ObjectResult[*core.Container]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &ctr,
		dagql.Selector{Field: "container"},
		dagql.Selector{Field: "withUnixSocket", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("/run/transfer.sock")},
			{Name: "source", Value: dagql.NewID[*core.Socket](socketID)},
		}}))
	return ctr
}

// withVolatile is container().withVolatileVariable("CI_RUN_ID", variant)
// .withEnvVariable("X", "y").
func withVolatile(t *testing.T, e *transferTestEngine, ctx context.Context, variant string) dagql.AnyResult {
	t.Helper()
	var ctr dagql.ObjectResult[*core.Container]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &ctr,
		dagql.Selector{Field: "container"},
		dagql.Selector{Field: "withVolatileVariable", Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString("CI_RUN_ID")},
			{Name: "value", Value: dagql.NewString(variant)},
		}},
		dagql.Selector{Field: "withEnvVariable", Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString("X")},
			{Name: "value", Value: dagql.NewString("y")},
		}}))
	return ctr
}

// withReplacedContent is directory().withDirectory("/", <source>), where
// the source, directory().withNewFile("a", variant), has the content digest
// transferSharedContent. The "replaced" variant's source is then taught
// another content digest, which replaces it, as a module's context directory
// from git.tree is re-taught with its snapshot hash.
func withReplacedContent(t *testing.T, e *transferTestEngine, ctx context.Context, variant string) dagql.AnyResult {
	t.Helper()
	var src dagql.ObjectResult[*core.Directory]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &src,
		dagql.Selector{Field: "directory"},
		dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("a")},
			{Name: "contents", Value: dagql.NewString(variant)},
		}}))
	src, err := src.WithContentDigest(ctx, transferSharedContent)
	require.NoError(t, err)
	if variant == "replaced" {
		src, err = src.WithContentDigest(ctx, digest.FromString("transfer-replacing-content"))
		require.NoError(t, err)
	}
	srcID, err := src.ID()
	require.NoError(t, err)
	var dir dagql.ObjectResult[*core.Directory]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &dir,
		dagql.Selector{Field: "directory"},
		dagql.Selector{Field: "withDirectory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("/")},
			{Name: "source", Value: dagql.NewID[*core.Directory](srcID)},
		}}))
	return dir
}

var transferSharedContent = digest.FromString("transfer-shared-content")

func transferTestSalt(t *testing.T) []byte {
	t.Helper()
	salt := make([]byte, 32)
	_, err := rand.Read(salt)
	require.NoError(t, err)
	return salt
}

// A result is exported from one engine and merged into another. There, a
// session makes a call that a second session on the first engine gets as a
// cache hit, and gets it as a hit too. Some of these calls meet the result
// only through an extra digest: a secret or socket handle, a volatile
// variable's call, or a content digest that a later one replaced. The content
// of a file taken from a source that changed elsewhere needs native snapshot
// mounts; TestUnchangedFileFromChangedSourceHitsOnAnotherEngine in
// core/integration covers it. Engines of one organization share one secret
// salt; under different salts a salted handle differs, and the call misses.
func TestEquivalentResultHitsAfterMergeIntoAnotherEngine(t *testing.T) {
	secretURI := withSecret(dagql.Selector{Field: "secret", Args: []dagql.NamedInput{
		{Name: "uri", Value: dagql.NewString("env://TRANSFER_SECRET")},
	}})
	for _, tc := range []struct {
		name     string
		build    transferTestBuild
		first    string
		then     string
		ownSalts bool
		hit      bool
	}{
		{name: "the same call, no extra digest", build: withEnv, first: "v", then: "v", hit: true},
		{name: "setSecret", build: withSecret(dagql.Selector{Field: "setSecret", Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString("transfer")},
			{Name: "plaintext", Value: dagql.NewString("transfer-plaintext")},
		}}), hit: true},
		{name: "secret(uri)", build: secretURI, hit: true},
		{name: "secret(uri, cacheKey)", build: withSecret(dagql.Selector{Field: "secret", Args: []dagql.NamedInput{
			{Name: "uri", Value: dagql.NewString("env://TRANSFER_SECRET")},
			{Name: "cacheKey", Value: dagql.Opt(dagql.NewString("transfer-cache-key"))},
		}}), hit: true},
		{name: "host.unixSocket", build: withHostSocket, hit: true},
		{name: "withVolatileVariable with a new value", build: withVolatile, first: "run-1", then: "run-2", hit: true},
		{name: "a content digest replaced after publication", build: withReplacedContent, first: "replaced", then: "kept", hit: true},
		{name: "secret(uri) under different salts", build: secretURI, ownSalts: true, hit: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saltA := transferTestSalt(t)
			saltB := saltA
			if tc.ownSalts {
				saltB = transferTestSalt(t)
			}
			a := newTransferTestEngine(t, saltA)
			b := newTransferTestEngine(t, saltB)

			actx := a.session(t, "a1")
			first := tc.build(t, a, actx, tc.first)
			require.False(t, first.HitCache(), "the first call runs")
			again := tc.build(t, a, a.session(t, "a2"), tc.then)
			require.True(t, again.HitCache(), "a second session on the same engine hits")

			bundle := a.export(t, actx, first)
			bctx := b.session(t, "b1")
			reply, err := b.cache.MergeValues(bctx, dagql.CloudCacheID, bundle)
			require.NoError(t, err)
			require.Len(t, reply.Imported(), 1, "the root lands on the other engine")
			onB := tc.build(t, b, bctx, tc.then)
			require.Equal(t, tc.hit, onB.HitCache(), "the same call on the other engine after the merge")
		})
	}
}
