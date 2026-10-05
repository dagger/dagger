package server

import (
	"context"
	"net"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type readPromptServer struct {
	prompt.UnimplementedPromptServer
	allow    map[string]bool
	requests []*prompt.BoolRequest
}

func (p *readPromptServer) PromptBool(_ context.Context, req *prompt.BoolRequest) (*prompt.BoolResponse, error) {
	p.requests = append(p.requests, req)
	return &prompt.BoolResponse{Response: p.allow[req.Prompt]}, nil
}

func TestGitReadApprovalOwnerBoundary(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	const (
		allowed = "https://example.com/allowed"
		denied  = "https://example.com/denied"
	)
	questions := &readPromptServer{allow: map[string]bool{
		"Allow an agent to read " + allowed + " with your Git credentials?": true,
	}}
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
	module.mod = sessionTestModuleResult(t, "staff")
	// A module-created container's nested API client is still delegated.
	nested := newClient("nested", "owner", "module")
	sess.clientRuntimes = map[string]*clientRuntime{"owner": owner, "module": module, "nested": nested}
	installTestClientRecords(sess)
	sess.attachables.callers["owner"] = &sessionAttachableCaller{ctx: t.Context(), conn: conn}
	srv := &Server{daggerSessions: map[string]*daggerSession{"session": sess}}
	clientContext := func(client *clientRuntime) context.Context {
		scope, err := sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "git read test")
		require.NoError(t, err)
		t.Cleanup(scope.Lease().Release)
		ctx, err := engine.ContextWithClientScope(t.Context(), scope)
		require.NoError(t, err)
		return ctx
	}
	ownerCtx := clientContext(owner)
	moduleCtx := clientContext(module)
	nestedCtx := clientContext(nested)

	// The owner's own conversation reads as the owner, without asking.
	md, err := srv.AuthorizeGitRead(ownerCtx, denied)
	require.NoError(t, err)
	require.Equal(t, owner.clientMetadata, md)
	require.Empty(t, questions.requests)

	// A module-driven agent borrows the owner's credentials only once the
	// owner approves that remote, asked once per session.
	for _, ctx := range []context.Context{moduleCtx, moduleCtx, nestedCtx} {
		md, err := srv.AuthorizeGitRead(ctx, allowed)
		require.NoError(t, err)
		require.Equal(t, owner.clientMetadata, md)
	}
	require.Len(t, questions.requests, 1)
	require.Equal(t, "Allow an agent to read "+allowed+" with your Git credentials?", questions.requests[0].Prompt)
	require.Empty(t, questions.requests[0].PersistentKey, "grants must not outlive the session")

	// A denial holds only for that attempt: saying no to course-correct must
	// not stop the agent from asking again later.
	for _, ctx := range []context.Context{moduleCtx, nestedCtx} {
		md, err := srv.AuthorizeGitRead(ctx, denied)
		require.ErrorContains(t, err, "denied by the owning client")
		require.Nil(t, md)
	}
	require.Len(t, questions.requests, 3)
}
