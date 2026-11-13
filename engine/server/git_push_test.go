package server

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type pushRoutingServer struct {
	git.UnimplementedGitServer // Credential/SSH RPCs must not run during authorization.
	entries                    []*git.GitConfigEntry
	requests                   int
	// checkouts is the config of the owner's own checkouts, by path.
	checkouts        map[string][]*git.GitConfigEntry
	checkoutRequests int
}

func (s *pushRoutingServer) GetConfig(_ context.Context, req *git.GitConfigRequest) (*git.GitConfigResponse, error) {
	if req.CheckoutPath != "" {
		entries, ok := s.checkouts[req.CheckoutPath]
		if !ok {
			return nil, errors.New("must not read a module checkout's config")
		}
		s.checkoutRequests++
		return &git.GitConfigResponse{Result: &git.GitConfigResponse_Config{Config: &git.GitConfig{Entries: entries}}}, nil
	}
	s.requests++
	return &git.GitConfigResponse{Result: &git.GitConfigResponse_Config{Config: &git.GitConfig{Entries: s.entries}}}, nil
}

// gitPushTestSession is a session with an owner able to answer prompts with
// questions, routing pushes with routing, and a module client it called.
type gitPushTestSession struct {
	srv                 *Server
	sess                *daggerSession
	owner               *clientRuntime
	ownerCtx, moduleCtx context.Context
	clientContext       func(*clientRuntime) context.Context
	newClient           func(id string, parents ...string) *clientRuntime
}

func newGitPushTestSession(t *testing.T, questions prompt.PromptServer, routing git.GitServer) *gitPushTestSession {
	t.Helper()
	conn := promptTestConn(t, func(srv *grpc.Server) {
		prompt.RegisterPromptServer(srv, questions)
		git.RegisterGitServer(srv, routing)
	})
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
	clientContext := func(client *clientRuntime) context.Context {
		scope, err := sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "git push test")
		require.NoError(t, err)
		t.Cleanup(scope.Lease().Release)
		ctx, err := engine.ContextWithClientScope(t.Context(), scope)
		require.NoError(t, err)
		return ctx
	}
	return &gitPushTestSession{
		srv: &Server{
			daggerSessions: map[string]*daggerSession{"session": sess},
			// No remote answers unless a test says so: every push asks as it
			// did before default branches were told apart.
			gitRemoteHead: func(context.Context, string) (string, error) { return "", core.ErrGitRemoteUnreachable },
		},
		sess:          sess,
		owner:         owner,
		ownerCtx:      clientContext(owner),
		moduleCtx:     clientContext(module),
		clientContext: clientContext,
		newClient:     newClient,
	}
}

func TestGitPushApprovalOwnerBoundary(t *testing.T) {
	// The owner grants pushes for the session and denies force-pushes.
	questions := &approvalPromptServer{decide: func(req *prompt.ApprovalRequest) prompt.ApprovalDecision {
		if req.Danger {
			return prompt.APPROVAL_DENY
		}
		return prompt.APPROVAL_ALLOW_SESSION
	}}
	routing := &pushRoutingServer{}
	s := newGitPushTestSession(t, questions, routing)
	srv, owner, moduleCtx := s.srv, s.owner, s.moduleCtx
	const remote = "git@example.com:repo"
	const ref = "refs/heads/main"
	const by = ` (requested by module "pusher")?`
	// Direct owner use is implicit authorization, including explicit leases.
	_, err := srv.AuthorizeGitPush(s.ownerCtx, remote, ref, true, false)
	require.NoError(t, err)
	require.Empty(t, questions.asked())
	for range 2 {
		md, err := srv.AuthorizeGitPush(moduleCtx, remote, ref, false, false)
		require.NoError(t, err)
		require.Equal(t, owner.clientMetadata, md.Owner)
		require.Equal(t, remote, md.Remote)
	}
	require.Len(t, questions.asked(), 1)
	req := questions.asked()[0]
	require.Equal(t, "Allow push to git@example.com:repo @ refs/heads/main"+by, req.Prompt)
	require.Equal(t, approvalKindGitPush, req.Kind)
	require.Equal(t, remote+" @ "+ref, req.Subject)
	require.False(t, req.Danger)
	for range 2 {
		md, err := srv.AuthorizeGitPush(moduleCtx, remote, ref, true, false)
		require.ErrorContains(t, err, "permission denied")
		require.Nil(t, md)
	}
	require.Len(t, questions.asked(), 3, "a denial is not remembered")
	req = questions.asked()[2]
	require.Equal(t, "Allow force-push to git@example.com:repo @ refs/heads/main"+by, req.Prompt)
	require.Equal(t, approvalKindGitForcePush, req.Kind)
	require.True(t, req.Danger)
	_, err = srv.AuthorizeGitPush(moduleCtx, remote, "refs/heads/other", false, false)
	require.NoError(t, err)
	require.Len(t, questions.asked(), 4)
	// A module-created container's nested API client is still delegated, and
	// asks as that module.
	nested := s.newClient("nested", "owner", "module")
	s.sess.clientRuntimes["nested"] = nested
	installTestClientRecords(s.sess)
	nestedCtx := s.clientContext(nested)
	md, err := srv.AuthorizeGitPush(nestedCtx, remote, ref, false, false)
	require.NoError(t, err)
	require.Equal(t, owner.clientMetadata, md.Owner)
	require.Equal(t, remote, md.Remote)
	_, err = srv.AuthorizeGitPush(nestedCtx, remote, ref, true, false)
	require.ErrorContains(t, err, "permission denied")
	require.Len(t, questions.asked(), 5)
	require.Zero(t, routing.requests, "explicit destinations must not consult owner rewrites")

	// Only the trusted owner has attachables. A nested module caller must use
	// that owner's routing and approve the actual endpoint, not the fetch URL.
	const fetchURL = "https://example.com/repo"
	for _, target := range []string{"git@push.example.com:", "git@other.example.com:"} {
		routing.entries = []*git.GitConfigEntry{{Key: "url." + target + ".pushinsteadof", Value: "https://example.com/"}}
		before := len(questions.asked())
		for range 2 {
			authorized, err := srv.AuthorizeGitPush(nestedCtx, fetchURL, ref, false, true)
			require.NoError(t, err)
			require.Equal(t, owner.clientMetadata, authorized.Owner)
			require.Equal(t, target+"repo", authorized.Remote)
		}
		require.Len(t, questions.asked(), before+1, "approval is scoped to the resolved destination")
		require.Equal(t, "Allow push to "+target+"repo @ "+ref+by, questions.asked()[before].Prompt)
	}
	for _, target := range []string{"https://sensitive-token@push.example.com/", "ssh://git:sensitive-password@push.example.com/", "https://push.example.com/?sensitive-token=", "file:///tmp/", "ext::sensitive-command "} {
		routing.entries = []*git.GitConfigEntry{{Key: "url." + target + ".pushinsteadof", Value: "https://example.com/"}}
		before := len(questions.asked())
		_, err := srv.AuthorizeGitPush(nestedCtx, fetchURL, ref, false, true)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "sensitive")
		require.Len(t, questions.asked(), before)
	}
}

func TestGitPushApprovalBoolPromptFallback(t *testing.T) {
	// A client predating approvals answers yes/no: a yes allows for the
	// session, as it did before approvals had a grant scope.
	questions := &boolPromptServer{answer: func(req *prompt.BoolRequest) bool {
		return req.Prompt == `Allow push to git@example.com:repo @ refs/heads/main (requested by module "pusher")?`
	}}
	s := newGitPushTestSession(t, questions, &pushRoutingServer{})
	const remote = "git@example.com:repo"
	const ref = "refs/heads/main"
	for range 2 {
		md, err := s.srv.AuthorizeGitPush(s.moduleCtx, remote, ref, false, false)
		require.NoError(t, err)
		require.Equal(t, s.owner.clientMetadata, md.Owner)
	}
	_, err := s.srv.AuthorizeGitPush(s.moduleCtx, remote, ref, true, false)
	require.ErrorContains(t, err, "permission denied")
	require.Len(t, questions.requests, 2, "a yes allows for the session")
	require.Equal(t, `Allow force-push to git@example.com:repo @ refs/heads/main (requested by module "pusher")?`, questions.requests[1].Prompt)
	for _, req := range questions.requests {
		require.Empty(t, req.PersistentKey)
	}
}

// yesHandler is a frontend that can only ask yes/no, like plain and dots, and
// says yes.
type yesHandler struct{ prompts []string }

func (h *yesHandler) HandlePrompt(_ context.Context, _, question string, dest any) error {
	h.prompts = append(h.prompts, question)
	*dest.(*bool) = true
	return nil
}

func (h *yesHandler) HandleForm(context.Context, *huh.Form) error { return errors.New("not a form") }

func TestGitPushApprovalYesNoFrontend(t *testing.T) {
	// The real attachable, with a frontend that has no ApprovalHandler.
	handler := &yesHandler{}
	s := newGitPushTestSession(t, prompt.NewPromptAttachable(handler), &pushRoutingServer{})
	for range 3 {
		_, err := s.srv.AuthorizeGitPush(s.moduleCtx, "git@example.com:repo", "refs/heads/main", false, false)
		require.NoError(t, err)
	}
	require.Len(t, handler.prompts, 1, "a yes allows for the session")
}

func TestGitPushApprovalPrompt(t *testing.T) {
	spec := gitPushApproval("https://example.com/\u202erepo", "refs/heads/x\x1b[31m", false)
	require.Equal(t, `Allow push to https://example.com/\u202erepo @ refs/heads/x\x1b[31m`, spec.Question)
	require.Equal(t, `https://example.com/\u202erepo @ refs/heads/x\x1b[31m`, spec.Subject)
	force := gitPushApproval("git@example.com:repo", "refs/heads/main", true)
	require.Equal(t, approvalSpec{
		Kind:     approvalKindGitForcePush,
		Subject:  "git@example.com:repo @ refs/heads/main",
		Question: "Allow force-push to git@example.com:repo @ refs/heads/main",
		Danger:   true,
	}, force)
}
