package server

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type pushPromptServer struct {
	prompt.UnimplementedPromptServer
	requests []*prompt.BoolRequest
}

func (p *pushPromptServer) PromptBool(_ context.Context, req *prompt.BoolRequest) (*prompt.BoolResponse, error) {
	p.requests = append(p.requests, req)
	return &prompt.BoolResponse{Response: !strings.Contains(req.Prompt, "force pushing")}, nil
}

func TestGitPushApprovalOwnerBoundary(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	questions := &pushPromptServer{}
	prompt.RegisterPromptServer(grpcServer, questions)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)
	conn, err := grpc.NewClient("passthrough:///prompt", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	sess := &daggerSession{sessionID: "session", attachables: newSessionAttachableManager()}
	sess.state.Store(sessionStateInitialized)
	newClient := func(id string, parents ...string) *clientRuntime {
		return &clientRuntime{clientRecord: &clientRecord{
			clientID: id, daggerSession: sess,
			clientMetadata:  &engine.ClientMetadata{ClientID: id, SessionID: "session"},
			parentClientIDs: parents, metadataSealed: true, accepting: true,
		}, state: clientStateInitialized, lifecycleLeases: make(map[uint64]clientLifecycleLeaseRecord)}
	}
	owner := newClient("owner")
	module := newClient("module", "owner")
	module.mod = sessionTestModuleResult(t, "pusher")
	sess.clientRuntimes = map[string]*clientRuntime{"owner": owner, "module": module}
	installTestClientRecords(sess)
	sess.attachables.callers["owner"] = &sessionAttachableCaller{ctx: t.Context(), conn: conn}
	srv := &Server{daggerSessions: map[string]*daggerSession{"session": sess}}
	clientContext := func(client *clientRuntime) context.Context {
		scope, err := sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "git push test")
		require.NoError(t, err)
		t.Cleanup(scope.Lease().Release)
		ctx, err := engine.ContextWithClientScope(t.Context(), scope)
		require.NoError(t, err)
		return ctx
	}
	ownerCtx := clientContext(owner)
	moduleCtx := clientContext(module)
	const remote = "git@example.com:repo"
	const ref = "refs/heads/main"
	// Direct owner use is implicit authorization, including explicit leases.
	_, err = srv.AuthorizeGitPush(ownerCtx, remote, ref, true)
	require.NoError(t, err)
	require.Empty(t, questions.requests)
	for range 2 {
		md, err := srv.AuthorizeGitPush(moduleCtx, remote, ref, false)
		require.NoError(t, err)
		require.Equal(t, owner.clientMetadata, md)
	}
	require.Len(t, questions.requests, 1)
	require.Equal(t, "Allow pushing to git@example.com:repo @ refs/heads/main?", questions.requests[0].Prompt)
	require.Empty(t, questions.requests[0].PersistentKey)
	for range 2 {
		md, err := srv.AuthorizeGitPush(moduleCtx, remote, ref, true)
		require.ErrorContains(t, err, "permission denied")
		require.Nil(t, md)
	}
	require.Len(t, questions.requests, 2)
	_, err = srv.AuthorizeGitPush(moduleCtx, remote, "refs/heads/other", false)
	require.NoError(t, err)
	require.Len(t, questions.requests, 3)
	// A module-created container's nested API client is still delegated.
	nested := newClient("nested", "owner", "module")
	sess.clientRuntimes["nested"] = nested
	installTestClientRecords(sess)
	nestedCtx := clientContext(nested)
	md, err := srv.AuthorizeGitPush(nestedCtx, remote, ref, false)
	require.NoError(t, err)
	require.Equal(t, owner.clientMetadata, md)
	_, err = srv.AuthorizeGitPush(nestedCtx, remote, ref, true)
	require.ErrorContains(t, err, "permission denied")
	require.Len(t, questions.requests, 3)
}

func TestGitPushApprovals(t *testing.T) {
	var approvals gitPushApprovals
	base := gitPushApprovalKey{owner: "owner", remote: "https://example.com/repo", ref: "refs/heads/main"}
	calls := 0
	ask := func(context.Context) (bool, error) { calls++; return true, nil }
	for range 2 {
		allowed, err := approvals.check(t.Context(), base, ask)
		require.NoError(t, err)
		require.True(t, allowed)
	}
	require.Equal(t, 1, calls)
	for _, key := range []gitPushApprovalKey{
		{owner: base.owner, remote: base.remote, ref: base.ref, force: true},
		{owner: base.owner, remote: base.remote, ref: "refs/heads/other"},
		{owner: base.owner, remote: "https://example.com/other", ref: base.ref},
		{owner: "other", remote: base.remote, ref: base.ref},
	} {
		_, err := approvals.check(t.Context(), key, ask)
		require.NoError(t, err)
	}
	require.Equal(t, 5, calls)
	var nextSession gitPushApprovals
	_, err := nextSession.check(t.Context(), base, ask)
	require.NoError(t, err)
	require.Equal(t, 6, calls, "grants must not survive a session")
}

func TestGitPushApprovalDenialAndCancellation(t *testing.T) {
	var approvals gitPushApprovals
	key := gitPushApprovalKey{remote: "git@example.com:repo", ref: "refs/heads/main"}
	_, err := approvals.check(t.Context(), key, func(context.Context) (bool, error) { return false, context.Canceled })
	require.ErrorIs(t, err, context.Canceled)
	calls := 0
	for range 2 {
		allowed, err := approvals.check(t.Context(), key, func(context.Context) (bool, error) { calls++; return false, nil })
		require.NoError(t, err)
		require.False(t, allowed)
	}
	require.Equal(t, 1, calls, "remember a user's No, but not canceled prompts")
}

func TestGitPushApprovalConcurrent(t *testing.T) {
	var approvals gitPushApprovals
	key := gitPushApprovalKey{remote: "https://example.com/repo", ref: "refs/heads/main"}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	ask := func(ctx context.Context) (bool, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			allowed, err := approvals.check(t.Context(), key, ask)
			if err != nil || !allowed {
				t.Errorf("approval = %v, %v", allowed, err)
			}
		})
	}
	<-started
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := approvals.check(canceled, key, ask)
	require.True(t, errors.Is(err, context.Canceled))
	close(release)
	wg.Wait()
	require.EqualValues(t, 1, calls.Load())
}
