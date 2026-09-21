package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// TestPersistedRemoteGitRepositoryRestoresAuthMirrorAndServices saves a remote
// Git repository whose backend holds its prepared mirror, authentication
// handles and service bindings, restarts, and checks that every one of those
// references is the exact retained row. Each declared reference is then
// dropped from the payload in turn to show the lost field is detected even
// while its dependency row survives.
func TestPersistedRemoteGitRepositoryRestoresAuthMirrorAndServices(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "git-a")
	env.manager.mutableBySnapshotID = map[string]bkcache.MutableRef{
		"bare-1": &cacheVolumeTestMutableRef{cacheVolumeTestImmutableRef: cacheVolumeTestImmutableRef{id: "bare-1", snapshotID: "bare-1"}},
	}
	ctx, cache, srv := env.open(t)

	remoteURL, err := gitutil.ParseURL("https://example.com/org/repo.git")
	require.NoError(t, err)
	mirror := NewRemoteGitMirror(remoteURL.Remote())
	mirror.snapshot = env.manager.mutableBySnapshotID["bare-1"]
	mirrorRes := env.attach(t, ctx, cache, srv, "git-mirror", mirror).(dagql.ObjectResult[*RemoteGitMirror])
	socketRes := env.attach(t, ctx, cache, srv, "git-ssh-socket", &Socket{Kind: SocketKindSSHHandle, Handle: "ssh-handle"}).(dagql.ObjectResult[*Socket])
	tokenRes := env.attach(t, ctx, cache, srv, "git-token", &Secret{Handle: "token-handle", NameVal: "token"}).(dagql.ObjectResult[*Secret])
	headerRes := env.attach(t, ctx, cache, srv, "git-header", &Secret{Handle: "header-handle", NameVal: "header"}).(dagql.ObjectResult[*Secret])
	serviceA := env.attach(t, ctx, cache, srv, "git-service-a", &Service{CustomHostname: "proxy-a", Args: []string{"a"}}).(dagql.ObjectResult[*Service])
	serviceB := env.attach(t, ctx, cache, srv, "git-service-b", &Service{CustomHostname: "proxy-b"}).(dagql.ObjectResult[*Service])
	ids := map[string]uint64{
		"mirror": persistedRowID(t, cache, mirrorRes), "socket": persistedRowID(t, cache, socketRes),
		"token": persistedRowID(t, cache, tokenRes), "header": persistedRowID(t, cache, headerRes),
		"service-a": persistedRowID(t, cache, serviceA), "service-b": persistedRowID(t, cache, serviceB),
	}

	backend := &RemoteGitRepository{
		URL:           remoteURL,
		SSHKnownHosts: "example.com ssh-ed25519 AAAA",
		AuthUsername:  "deploy",
		Platform:      Platform{OS: "linux", Architecture: "amd64"},
		Mirror:        mirrorRes,
		SSHAuthSocket: socketRes,
		AuthToken:     tokenRes,
		AuthHeader:    headerRes,
		Services: ServiceBindings{
			{Service: serviceA, Hostname: "a.internal", Aliases: AliasSet{"alpha", "first"}},
			{Service: serviceB, Hostname: "b.internal"},
		},
	}
	// Production repositories start with empty remote metadata (see
	// NewGitRepository); the fixture matches that shape so the untouched
	// second save compares like-for-like.
	repoRes := env.attach(t, ctx, cache, srv, "git-repo", &GitRepository{URL: dagql.NonNull(dagql.String(remoteURL.String())), Backend: backend, Remote: &gitutil.Remote{}})
	repoID := persistedRowID(t, cache, repoRes)
	repoFrame, err := repoRes.ResultCall()
	require.NoError(t, err)
	encoding := persistedEncoding(t, ctx, cache, repoRes)

	refs := assertPersistedRefsMatchOwnership(t, ctx, cache, repoRes)
	require.Equal(t, map[string]uint64{
		"objectJSON.remote.mirrorResultID":              ids["mirror"],
		"objectJSON.remote.sshAuthSocketResultID":       ids["socket"],
		"objectJSON.remote.authTokenResultID":           ids["token"],
		"objectJSON.remote.authHeaderResultID":          ids["header"],
		"objectJSON.remote.services[0].serviceResultID": ids["service-a"],
		"objectJSON.remote.services[1].serviceResultID": ids["service-b"],
	}, refs, "every declared reference position is enumerated once")

	assertRestored := func(t *testing.T, restored *RemoteGitRepository) {
		t.Helper()
		require.Equal(t, remoteURL.String(), restored.URL.String())
		require.Equal(t, "example.com ssh-ed25519 AAAA", restored.SSHKnownHosts)
		require.Equal(t, "deploy", restored.AuthUsername)
		require.Equal(t, Platform{OS: "linux", Architecture: "amd64"}, restored.Platform)
		require.Equal(t, ids["mirror"], persistedRowID(t, cache, restored.Mirror), "the exact prepared mirror row, not a URL selection")
		require.Equal(t, remoteURL.Remote(), restored.Mirror.Self().RemoteURL)
		require.Equal(t, ids["socket"], persistedRowID(t, cache, restored.SSHAuthSocket))
		require.Equal(t, SocketKindSSHHandle, restored.SSHAuthSocket.Self().Kind)
		require.Equal(t, dagql.SessionResourceHandle("ssh-handle"), restored.SSHAuthSocket.Self().Handle, "the socket stays a fresh-binding handle")
		require.Equal(t, ids["token"], persistedRowID(t, cache, restored.AuthToken))
		require.Equal(t, dagql.SessionResourceHandle("token-handle"), restored.AuthToken.Self().Handle, "authentication selects the token handle at use time")
		require.Equal(t, "token", restored.AuthToken.Self().NameVal)
		require.Empty(t, restored.AuthToken.Self().PlaintextVal, "no plaintext credential is persisted")
		require.Equal(t, ids["header"], persistedRowID(t, cache, restored.AuthHeader))
		require.Equal(t, dagql.SessionResourceHandle("header-handle"), restored.AuthHeader.Self().Handle)
		require.Len(t, restored.Services, 2)
		require.Equal(t, ids["service-a"], persistedRowID(t, cache, restored.Services[0].Service))
		require.Equal(t, "a.internal", restored.Services[0].Hostname)
		require.Equal(t, AliasSet{"alpha", "first"}, restored.Services[0].Aliases)
		require.Equal(t, "proxy-a", restored.Services[0].Service.Self().CustomHostname)
		require.Equal(t, ids["service-b"], persistedRowID(t, cache, restored.Services[1].Service), "service order survives")
		require.Equal(t, "b.internal", restored.Services[1].Hostname)
		require.Empty(t, restored.Services[1].Aliases)
	}

	for round := 1; round <= 2; round++ {
		ctx, cache, srv = env.restart(t, ctx, cache)
		loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, repoID)
		require.NoError(t, err)
		repo := loaded.Unwrap().(*GitRepository)
		require.Equal(t, remoteURL.String(), repo.URL.Value.String())
		restored, ok := repo.Backend.(*RemoteGitRepository)
		require.True(t, ok)
		assertRestored(t, restored)
		require.Contains(t, env.manager.getMutableBySnapshotIDCalls, "bare-1", "the mirror's local backing is reopened by its saved role key")
		require.Equal(t, encoding.Envelope, persistedEncoding(t, ctx, cache, loaded).Envelope, "round %d: the second save is identical", round)
	}

	// Deliberate controls: each dropped reference is a detectable loss even
	// though the dependency row itself still exists and loads.
	var payload map[string]any
	require.NoError(t, json.Unmarshal(encoding.Envelope.ObjectJSON, &payload))
	remote, ok := payload["remote"].(map[string]any)
	require.True(t, ok)
	for _, tc := range []struct {
		key    string
		depID  uint64
		absent func(t *testing.T, restored *RemoteGitRepository)
	}{
		{"mirrorResultID", ids["mirror"], func(t *testing.T, r *RemoteGitRepository) { require.Nil(t, r.Mirror.Self()) }},
		{"sshAuthSocketResultID", ids["socket"], func(t *testing.T, r *RemoteGitRepository) { require.Nil(t, r.SSHAuthSocket.Self()) }},
		{"authTokenResultID", ids["token"], func(t *testing.T, r *RemoteGitRepository) { require.Nil(t, r.AuthToken.Self()) }},
		{"authHeaderResultID", ids["header"], func(t *testing.T, r *RemoteGitRepository) { require.Nil(t, r.AuthHeader.Self()) }},
		{"services", ids["service-a"], func(t *testing.T, r *RemoteGitRepository) { require.Empty(t, r.Services) }},
	} {
		t.Run("dropping "+tc.key, func(t *testing.T) {
			lossyRemote := map[string]any{}
			for k, v := range remote {
				if k != tc.key {
					lossyRemote[k] = v
				}
			}
			lossy := map[string]any{}
			for k, v := range payload {
				lossy[k] = v
			}
			lossy["remote"] = lossyRemote
			lossyJSON, err := json.Marshal(lossy)
			require.NoError(t, err)
			decoded, err := (&GitRepository{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, repoID, repoFrame), lossyJSON)
			require.NoError(t, err)
			restored := decoded.(*GitRepository).Backend.(*RemoteGitRepository)
			tc.absent(t, restored)
			dep, err := cache.LoadResultByResultID(ctx, env.session, srv, tc.depID)
			require.NoError(t, err, "the dependency row survives; only the field reference was lost")
			require.NotNil(t, dep)
			exact, err := (&GitRepository{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, repoID, repoFrame), encoding.Envelope.ObjectJSON)
			require.NoError(t, err)
			assertRestored(t, exact.(*GitRepository).Backend.(*RemoteGitRepository))
		})
	}
}

// TestPersistedRemoteGitRepositoryUsesFreshSessionResources restores a remote
// Git repository whose credentials and SSH socket are session-resource
// handles, then has a later session bind its own material for those handles
// and consume it. Ordinary use reads these exact fields when it authenticates
// and mounts the agent (core/git_remote.go), so restoring the handle string
// alone is not enough: the restored repository has to select the material the
// using session bound, and a session that bound nothing has to be refused.
// No Git server or network request is involved; the fresh material comes from
// in-process attachable servers.
func TestPersistedRemoteGitRepositoryUsesFreshSessionResources(t *testing.T) {
	tokenConn := newTestAttachableConn(t, func(server *grpc.Server) {
		secrets.RegisterSecretsServer(server, &testSecretsServer{data: []byte("fresh-token-plaintext")})
	})
	headerConn := newTestAttachableConn(t, func(server *grpc.Server) {
		secrets.RegisterSecretsServer(server, &testSecretsServer{data: []byte("Bearer fresh-header")})
	})
	sshSrv := &testSSHServer{}
	sshConn := newTestAttachableConn(t, func(server *grpc.Server) {
		sshforward.RegisterSSHServer(server, sshSrv)
	})

	env := newPersistedFamiliesTestEnv(t, "git-fresh-a")
	env.attachables = map[string]*grpc.ClientConn{
		"fresh-token-client":  tokenConn,
		"fresh-header-client": headerConn,
		"fresh-ssh-client":    sshConn,
	}
	ctx, cache, srv := env.open(t)

	const tokenHandle = dagql.SessionResourceHandle("git-fresh-token-handle")
	const headerHandle = dagql.SessionResourceHandle("git-fresh-header-handle")
	const socketHandle = dagql.SessionResourceHandle("git-fresh-socket-handle")

	remoteURL, err := gitutil.ParseURL("https://example.com/org/repo.git")
	require.NoError(t, err)
	tokenRes := env.attachWithResourceHandle(t, ctx, cache, srv, "git-fresh-token", &Secret{Handle: tokenHandle, NameVal: "token"}, tokenHandle).(dagql.ObjectResult[*Secret])
	headerRes := env.attachWithResourceHandle(t, ctx, cache, srv, "git-fresh-header", &Secret{Handle: headerHandle, NameVal: "header"}, headerHandle).(dagql.ObjectResult[*Secret])
	socketRes := env.attachWithResourceHandle(t, ctx, cache, srv, "git-fresh-socket", &Socket{Kind: SocketKindSSHHandle, Handle: socketHandle}, socketHandle).(dagql.ObjectResult[*Socket])
	serviceRes := env.attach(t, ctx, cache, srv, "git-fresh-service", &Service{CustomHostname: "proxy", Args: []string{"serve"}}).(dagql.ObjectResult[*Service])

	fresh := &RemoteGitRepository{
		URL:           remoteURL,
		AuthUsername:  "deploy",
		Platform:      Platform{OS: "linux", Architecture: "amd64"},
		AuthToken:     tokenRes,
		AuthHeader:    headerRes,
		SSHAuthSocket: socketRes,
		Services:      ServiceBindings{{Service: serviceRes, Hostname: "proxy.internal", Aliases: AliasSet{"proxy"}}},
	}
	freshScope := fresh.remoteCacheScope()
	repoRes := env.attach(t, ctx, cache, srv, "git-fresh-repo", &GitRepository{URL: dagql.NonNull(dagql.String(remoteURL.String())), Backend: fresh, Remote: &gitutil.Remote{}})
	repoID := persistedRowID(t, cache, repoRes)
	serviceID := persistedRowID(t, cache, serviceRes)

	ctx, cache, srv = env.restart(t, ctx, cache)

	// A session that bound nothing cannot use the repository: the credential
	// and socket requirements survived the restart with it.
	_, err = cache.LoadResultByResultID(ctx, "git-unbound-session", srv, repoID)
	require.ErrorContains(t, err, "has not bound the session resources this result requires")

	bindCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "fresh-token-client", SessionID: env.session})
	require.NoError(t, cache.BindSessionResource(bindCtx, env.session, "fresh-token-client", tokenHandle, &Secret{URIVal: "env://GIT_TOKEN", SourceClientID: "fresh-token-client"}))
	require.NoError(t, cache.BindSessionResource(bindCtx, env.session, "fresh-header-client", headerHandle, &Secret{URIVal: "env://GIT_HEADER", SourceClientID: "fresh-header-client"}))
	require.NoError(t, cache.BindSessionResource(bindCtx, env.session, "fresh-ssh-client", socketHandle, &Socket{Kind: SocketKindUnixOpaque, URLVal: "unix:///tmp/fresh-agent.sock", SourceClientID: "fresh-ssh-client"}))

	loaded, err := cache.LoadResultByResultID(bindCtx, env.session, srv, repoID)
	require.NoError(t, err)
	restored, ok := loaded.Unwrap().(*GitRepository).Backend.(*RemoteGitRepository)
	require.True(t, ok)

	// Authentication selection: the restored fields are the ones the use path
	// reads, and they still name the same credentials.
	require.Equal(t, freshScope, restored.remoteCacheScope(), "restored credentials select the same authentication scope")
	require.Contains(t, restored.remoteCacheScope(), "token:"+string(tokenHandle))
	require.Contains(t, restored.remoteCacheScope(), "header:"+string(headerHandle))
	require.Contains(t, restored.remoteCacheScope(), "ssh-auth-scope:"+string(socketHandle))
	require.Contains(t, restored.remoteCacheScope(), "username:deploy")

	// Fresh material: the restored handles resolve to what this session bound,
	// which is not what the producing session held.
	token, err := restored.AuthToken.Self().Plaintext(bindCtx)
	require.NoError(t, err)
	require.Equal(t, []byte("fresh-token-plaintext"), token, "the restored token reads this session's material")
	header, err := restored.AuthHeader.Self().Plaintext(bindCtx)
	require.NoError(t, err)
	require.Equal(t, []byte("Bearer fresh-header"), header, "each credential resolves its own binding")

	// The SSH socket is consumed, not just mounted: forwarding only reaches a
	// client when the agent stream opens.
	stream, err := restored.SSHAuthSocket.Self().ForwardAgentClient(bindCtx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&sshforward.BytesMessage{Data: []byte("agent")}))
	msg, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, []byte("ok:agent"), msg.Data)
	require.NoError(t, stream.CloseSend())
	require.Equal(t, int32(1), sshSrv.calls.Load(), "the restored socket reached this session's agent")

	// Service inputs: the bindings the use path hands to service startup are
	// the exact restored rows, with their hostname and aliases. No service is
	// started here.
	require.Len(t, restored.Services, 1)
	require.Equal(t, serviceID, persistedRowID(t, cache, restored.Services[0].Service))
	require.Equal(t, "proxy.internal", restored.Services[0].Hostname)
	require.Equal(t, AliasSet{"proxy"}, restored.Services[0].Aliases)
	require.Equal(t, []string{"serve"}, restored.Services[0].Service.Self().Args)
	owned, err := restored.Services.AttachDependencyResults("remote git", func(dep dagql.AnyResult) (dagql.AnyResult, error) { return dep, nil })
	require.NoError(t, err)
	require.Len(t, owned, 1)
	require.Equal(t, serviceID, persistedRowID(t, cache, owned[0].Result), "the restored binding owns the service row it will start")

	// Deliberate controls: a credential whose binding is missing fails at use,
	// and a dropped credential changes the authentication selection even
	// though the rest of the repository restores.
	t.Run("an unbound handle fails at use", func(t *testing.T) {
		otherCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "fresh-token-client", SessionID: "git-partial-session"})
		require.NoError(t, cache.BindSessionResource(otherCtx, "git-partial-session", "fresh-token-client", tokenHandle, &Secret{URIVal: "env://GIT_TOKEN", SourceClientID: "fresh-token-client"}))
		_, err := restored.AuthHeader.Self().Plaintext(otherCtx)
		require.ErrorContains(t, err, "no bound resource for session")
	})
	t.Run("a dropped credential changes the selection", func(t *testing.T) {
		without := *restored
		without.AuthToken = dagql.ObjectResult[*Secret]{}
		require.NotEqual(t, freshScope, without.remoteCacheScope())
		require.NotContains(t, without.remoteCacheScope(), "token:"+string(tokenHandle))
	})
}
