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
	// otherCtx is another module the owner called; impostorCtx another module
	// named like the first, loaded from another source.
	otherCtx, impostorCtx context.Context
}

// newGitReadTestSession is a session with an owner able to answer prompts, a
// module client it called, that module's nested API client, and two other
// module clients it called.
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
	module.mod = sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", "1111111111111111111111111111111111111111")
	// A module-created container's nested API client is still delegated.
	nested := newClient("nested", "owner", "module")
	other := newClient("other", "owner")
	other.mod = sessionTestModuleResult(t, "other")
	impostor := newClient("impostor", "owner")
	impostor.mod = sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/evil/staff", "2222222222222222222222222222222222222222")
	sess.clientRuntimes = map[string]*clientRuntime{
		"owner": owner, "module": module, "nested": nested, "other": other, "impostor": impostor,
	}
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
		srv:         &Server{daggerSessions: map[string]*daggerSession{"session": sess}},
		questions:   questions,
		owner:       owner,
		ownerCtx:    clientContext(owner),
		moduleCtx:   clientContext(module),
		nested:      clientContext(nested),
		otherCtx:    clientContext(other),
		impostorCtx: clientContext(impostor),
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

func TestGitReadApprovalPerModule(t *testing.T) {
	const remote = "https://example.com/repo"
	const staffPrompt = "Allow an agent to read " + remote + ` with your Git credentials (requested by module "staff")?`
	const otherPrompt = "Allow an agent to read " + remote + ` with your Git credentials (requested by module "other")?`
	s := newGitReadTestSession(t, map[string]bool{staffPrompt: true, otherPrompt: true})

	_, err := s.srv.AuthorizeGitRead(s.moduleCtx, remote)
	require.NoError(t, err)
	require.Equal(t, []string{staffPrompt}, s.questions.prompts())

	// The prompt named the module, so the grant is that module's: another
	// module reading the same remote is asked about separately.
	for range 2 {
		md, err := s.srv.AuthorizeGitRead(s.otherCtx, remote)
		require.NoError(t, err)
		require.Equal(t, s.owner.clientMetadata, md)
	}
	require.Equal(t, []string{staffPrompt, otherPrompt}, s.questions.prompts())

	// A module only named like the approved one is not it.
	_, err = s.srv.AuthorizeGitRead(s.impostorCtx, remote)
	require.NoError(t, err)
	require.Equal(t, []string{staffPrompt, otherPrompt, staffPrompt}, s.questions.prompts())

	// The original grants still hold.
	for _, ctx := range []context.Context{s.moduleCtx, s.nested, s.otherCtx} {
		_, err := s.srv.AuthorizeGitRead(ctx, remote)
		require.NoError(t, err)
	}
	require.Len(t, s.questions.requests, 3)
}

func TestGitReadApprovalSSH(t *testing.T) {
	const (
		allowed = "git@example.com:org/allowed"
		denied  = "ssh://git@example.com/org/denied"
	)
	const allowedPrompt = "Allow an agent to read " + allowed + " with your SSH keys?"
	const moduleAllowedPrompt = "Allow an agent to read " + allowed + ` with your SSH keys (requested by module "staff")?`
	s := newGitReadTestSession(t, map[string]bool{allowedPrompt: true, moduleAllowedPrompt: true})

	// An SSH agent is not scoped to a host, so even the owner's own
	// conversation asks before offering the owner's keys to a model's choice.
	for range 2 {
		md, err := s.srv.AuthorizeGitRead(s.ownerCtx, allowed)
		require.NoError(t, err)
		require.Equal(t, s.owner.clientMetadata, md)
	}
	require.Equal(t, []string{allowedPrompt}, s.questions.prompts(), "a grant lasts for the session")
	for range 2 {
		md, err := s.srv.AuthorizeGitRead(s.ownerCtx, denied)
		require.ErrorContains(t, err, "denied by the owning client")
		require.Nil(t, md)
	}
	require.Len(t, s.questions.requests, 3, "a denial is not remembered")

	// The owner's own grant is not a module's: a module driving an agent is
	// asked about separately, by name.
	for range 2 {
		md, err := s.srv.AuthorizeGitRead(s.moduleCtx, allowed)
		require.NoError(t, err)
		require.Equal(t, s.owner.clientMetadata, md)
	}
	require.Len(t, s.questions.requests, 4)
	require.Equal(t, moduleAllowedPrompt, s.questions.requests[3].Prompt)
	_, err := s.srv.AuthorizeGitRead(s.moduleCtx, denied)
	require.ErrorContains(t, err, "denied by the owning client")
	require.Equal(t, "Allow an agent to read "+denied+` with your SSH keys (requested by module "staff")?`,
		s.questions.requests[4].Prompt)
}

func TestGitReadApprovalNormalizesRemote(t *testing.T) {
	const canonical = "ssh://git@example.com/org/repo"
	const httpsPrompt = `Allow an agent to read https://example.com/org/repo with your Git credentials (requested by module "staff")?`
	s := newGitReadTestSession(t, map[string]bool{
		"Allow an agent to read " + canonical + " with your SSH keys?": true,
		httpsPrompt: true,
	})
	for _, remote := range []string{
		canonical,
		"ssh://example.com/org/repo",
		"ssh://git@Example.COM/org/repo.git",
		"ssh://git@example.com/org/repo/",
		"ssh://git@example.com/org/repo.git/",
	} {
		_, err := s.srv.AuthorizeGitRead(s.ownerCtx, remote)
		require.NoError(t, err, remote)
	}
	require.Len(t, s.questions.requests, 1, "spellings of one repository share a grant")

	// HTTPS authenticates with other credentials, so it is approved apart.
	for _, remote := range []string{"https://EXAMPLE.com/org/repo.git", "https://example.com/org/repo/"} {
		_, err := s.srv.AuthorizeGitRead(s.moduleCtx, remote)
		require.NoError(t, err, remote)
	}
	require.Len(t, s.questions.requests, 2)

	// Another user, host or path is another grant.
	for _, remote := range []string{
		"ssh://deploy@example.com/org/repo",
		"ssh://git@example.org/org/repo",
		"ssh://git@example.com/org/other",
	} {
		_, err := s.srv.AuthorizeGitRead(s.ownerCtx, remote)
		require.ErrorContains(t, err, "denied by the owning client", remote)
	}
	require.Len(t, s.questions.requests, 5)
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
	got := gitReadPrompt("https://example.com/\u202erepo", false, "mod\x1b[31m")
	require.Equal(t, `Allow an agent to read https://example.com/\u202erepo with your Git credentials (requested by module "mod\x1b[31m")?`, got)
	require.True(t, strings.HasPrefix(got, gitReadPromptPrefix))
}
