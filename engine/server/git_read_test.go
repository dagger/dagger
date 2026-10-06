package server

import (
	"context"
	"net"
	"strings"
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

func (p *readPromptServer) prompts() []string {
	var prompts []string
	for _, req := range p.requests {
		prompts = append(prompts, req.Prompt)
	}
	return prompts
}

type gitReadTestSession struct {
	srv                         *Server
	questions                   *readPromptServer
	owner                       *clientRuntime
	ownerCtx, moduleCtx, nested context.Context
}

// newGitReadTestSession is a session with an owner able to answer prompts, a
// module client it called, and that module's nested API client.
func newGitReadTestSession(t *testing.T, allow map[string]bool) *gitReadTestSession {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	questions := &readPromptServer{allow: allow}
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
	clientContext := func(client *clientRuntime) context.Context {
		scope, err := sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "git read test")
		require.NoError(t, err)
		t.Cleanup(scope.Lease().Release)
		ctx, err := engine.ContextWithClientScope(t.Context(), scope)
		require.NoError(t, err)
		return ctx
	}
	return &gitReadTestSession{
		srv:       &Server{daggerSessions: map[string]*daggerSession{"session": sess}},
		questions: questions,
		owner:     owner,
		ownerCtx:  clientContext(owner),
		moduleCtx: clientContext(module),
		nested:    clientContext(nested),
	}
}

func TestGitReadApprovalOwnerBoundary(t *testing.T) {
	const (
		allowed = "https://example.com/allowed"
		denied  = "https://example.com/denied"
	)
	const allowedPrompt = "Allow an agent to read " + allowed + ` with your Git credentials (requested by module "staff")?`
	s := newGitReadTestSession(t, map[string]bool{allowedPrompt: true})

	// The owner's own conversation reads HTTP(S) as the owner, without asking.
	md, err := s.srv.AuthorizeGitRead(s.ownerCtx, denied)
	require.NoError(t, err)
	require.Equal(t, s.owner.clientMetadata, md)
	require.Empty(t, s.questions.requests)

	// A module-driven agent borrows the owner's credentials only once the
	// owner approves that remote, asked once per session.
	for _, ctx := range []context.Context{s.moduleCtx, s.moduleCtx, s.nested} {
		md, err := s.srv.AuthorizeGitRead(ctx, allowed)
		require.NoError(t, err)
		require.Equal(t, s.owner.clientMetadata, md)
	}
	require.Equal(t, []string{allowedPrompt}, s.questions.prompts(),
		"the prompt names the module driving the agent, even from its nested client")
	require.Empty(t, s.questions.requests[0].PersistentKey, "grants must not outlive the session")

	// A denial holds only for that attempt: saying no to course-correct must
	// not stop the agent from asking again later.
	for _, ctx := range []context.Context{s.moduleCtx, s.nested} {
		md, err := s.srv.AuthorizeGitRead(ctx, denied)
		require.ErrorContains(t, err, "denied by the owning client")
		require.Nil(t, md)
	}
	require.Len(t, s.questions.requests, 3)
}

func TestGitReadApprovalNormalizesRemote(t *testing.T) {
	const canonical = "https://example.com/org/repo"
	s := newGitReadTestSession(t, map[string]bool{
		"Allow an agent to read " + canonical + ` with your Git credentials (requested by module "staff")?`: true,
	})
	for _, remote := range []string{
		canonical,
		"https://Example.COM/org/repo.git",
		"https://example.com/org/repo/",
		"https://example.com/org/repo.git/",
	} {
		_, err := s.srv.AuthorizeGitRead(s.moduleCtx, remote)
		require.NoError(t, err, remote)
	}
	require.Len(t, s.questions.requests, 1, "spellings of one repository share a grant")

	// Another scheme, host or path is another grant.
	for _, remote := range []string{
		"http://example.com/org/repo",
		"https://example.org/org/repo",
		"https://example.com/org/other",
	} {
		_, err := s.srv.AuthorizeGitRead(s.moduleCtx, remote)
		require.ErrorContains(t, err, "denied by the owning client", remote)
	}
	require.Len(t, s.questions.requests, 4)
}

func TestGitReadApprovalRejectsRemote(t *testing.T) {
	s := newGitReadTestSession(t, nil)
	for _, remote := range []string{
		"https://sensitive-token@example.com/repo",
		"https://user:sensitive-password@example.com/repo",
		"ssh://git:sensitive-password@example.com/repo",
		"https://example.com/repo?sensitive-token=",
		"https://example.com/repo#sensitive",
		"https://example.com/repo\nsensitive",
		"https://example.com/repo\rsensitive",
		"https://example.com/repo\x00sensitive",
		"https:///repo",
		"https://example.com/",
		"file:///tmp/repo",
		"/tmp/repo",
		"ext::sensitive-command",
	} {
		for _, ctx := range []context.Context{s.ownerCtx, s.moduleCtx} {
			md, err := s.srv.AuthorizeGitRead(ctx, remote)
			require.Error(t, err, remote)
			require.Nil(t, md)
			require.NotContains(t, err.Error(), "sensitive", "never echo a rejected remote")
		}
	}
	require.Empty(t, s.questions.requests, "reject before prompting")
}

func TestGitReadPromptEscapes(t *testing.T) {
	got := gitReadPrompt("https://example.com/\u202erepo", "mod\x1b[31m")
	require.Equal(t, `Allow an agent to read https://example.com/\u202erepo with your Git credentials (requested by module "mod\x1b[31m")?`, got)
	require.True(t, strings.HasPrefix(got, gitReadPromptPrefix))
}
