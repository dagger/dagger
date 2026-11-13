package server

import (
	"context"
	"sync"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// identityGitServer is an owner's client serving its Git config.
type identityGitServer struct {
	git.UnimplementedGitServer // Credential/SSH RPCs must not run.
	entries                    []*git.GitConfigEntry
	gitErr                     *git.ErrorInfo

	mu    sync.Mutex
	paths []string
}

func (s *identityGitServer) GetConfig(_ context.Context, req *git.GitConfigRequest) (*git.GitConfigResponse, error) {
	s.mu.Lock()
	s.paths = append(s.paths, req.CheckoutPath)
	s.mu.Unlock()
	if s.gitErr != nil {
		return &git.GitConfigResponse{Result: &git.GitConfigResponse_Error{Error: s.gitErr}}, nil
	}
	return &git.GitConfigResponse{Result: &git.GitConfigResponse_Config{Config: &git.GitConfig{Entries: s.entries}}}, nil
}

func identityEntries(kv ...string) []*git.GitConfigEntry {
	var entries []*git.GitConfigEntry
	for i := 0; i < len(kv); i += 2 {
		entries = append(entries, &git.GitConfigEntry{Key: kv[i], Value: kv[i+1]})
	}
	return entries
}

func newGitIdentityTestSession(t *testing.T, questions prompt.PromptServer, config *identityGitServer) *approvalTestSession {
	t.Helper()
	return newApprovalTestSession(t, func(srv *grpc.Server) {
		if questions != nil {
			prompt.RegisterPromptServer(srv, questions)
		}
		if config != nil {
			git.RegisterGitServer(srv, config)
		}
	})
}

func TestGitIdentityApprovalOwnerBoundary(t *testing.T) {
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return prompt.APPROVAL_ALLOW_SESSION }}
	config := &identityGitServer{entries: identityEntries(
		"user.name", "Global", "user.email", "global@example.com",
		"User.Name", "Personal", "user.email", "personal@example.com",
	)}
	s := newGitIdentityTestSession(t, questions, config)
	want := core.GitIdentity{Name: "Personal", Email: "personal@example.com"}

	// The owner's own direct call shares its identity without asking: the
	// effective config at its working directory, where the last value wins.
	identity, err := s.srv.AuthorizeGitIdentity(s.ctx(s.owner))
	require.NoError(t, err)
	require.Equal(t, want, identity)
	require.Empty(t, questions.asked())
	require.Equal(t, []string{"."}, config.paths)

	// A module asks first, showing exactly what is shared; a session grant
	// holds across the module's functions, and its nested clients read the
	// owner's identity, not their own.
	staff := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin)
	commit := s.client("commit", staff, &core.FunctionCall{ParentName: "Staff", Name: "commit"}, "owner")
	pull := s.client("pull", staff, &core.FunctionCall{ParentName: "Staff", Name: "pull"}, "owner")
	nested := s.client("nested", dagql.ObjectResult[*core.Module]{}, nil, "owner", "pull")
	for _, client := range []*clientRuntime{commit, pull, nested} {
		identity, err := s.srv.AuthorizeGitIdentity(s.ctx(client))
		require.NoError(t, err)
		require.Equal(t, want, identity)
	}
	require.Len(t, questions.asked(), 1)
	req := questions.asked()[0]
	require.Equal(t, approvalKindGitIdentity, req.Kind)
	require.Equal(t, "Personal <personal@example.com>", req.Subject)
	require.False(t, req.Danger)
	require.Equal(t, `Allow using your Git identity "Personal <personal@example.com>" (requested by module "staff", function "Staff.commit")?`, req.Prompt)
	require.Equal(t, `approval:v1:"git-identity":"https://github.com/org/staff|staff":"":"Personal <personal@example.com>"`, req.PersistentKey)

	// The grant is for the identity the owner saw: once they change it, the
	// module asks again.
	config.entries = identityEntries("user.name", "Work", "user.email", "work@example.com")
	identity, err = s.srv.AuthorizeGitIdentity(s.ctx(commit))
	require.NoError(t, err)
	require.Equal(t, core.GitIdentity{Name: "Work", Email: "work@example.com"}, identity)
	require.Len(t, questions.asked(), 2)
	require.Equal(t, "Work <work@example.com>", questions.asked()[1].Subject)
	config.entries = identityEntries("user.name", "Personal", "user.email", "personal@example.com")

	// An agent in the owner's own conversation asks too, as itself.
	ctx := core.AgentToContext(s.ctx(s.owner), testAgent(t, "agent-1", "worker"))
	identity, err = s.srv.AuthorizeGitIdentity(ctx)
	require.NoError(t, err)
	require.Equal(t, want, identity)
	require.Len(t, questions.asked(), 3)
	require.Equal(t, `Allow using your Git identity "Personal <personal@example.com>" (requested by agent "worker")?`, questions.asked()[2].Prompt)
	require.Empty(t, questions.asked()[2].PersistentKey, "an agent's name alone is not an identity to remember")
}

func TestGitIdentityApprovalDenied(t *testing.T) {
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return prompt.APPROVAL_DENY }}
	config := &identityGitServer{entries: identityEntries("user.name", "Personal", "user.email", "personal@example.com")}
	s := newGitIdentityTestSession(t, questions, config)
	module := s.client("module", sessionTestModuleResult(t, "committer"), nil, "owner")
	for range 2 {
		identity, err := s.srv.AuthorizeGitIdentity(s.ctx(module))
		require.ErrorIs(t, err, core.ErrGitIdentityNotShared)
		require.Zero(t, identity, "a denied identity is never handed over")
	}
	require.Len(t, questions.asked(), 2, "a denial is not remembered")
}

// withCommitCall is ctx evaluating Workspace.withCommit with message.
func withCommitCall(t *testing.T, ctx context.Context, message string) context.Context {
	t.Helper()
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	return dagql.ContextWithCall(dagql.ContextWithCache(ctx, cache), &dagql.ResultCall{
		Kind:  dagql.ResultCallKindField,
		Type:  dagql.NewResultCallType((&core.Workspace{}).Type()),
		Field: "withCommit",
		Args:  []*dagql.ResultCallArg{{Name: "message", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: message}}},
	})
}

func TestGitIdentityApprovalSameCall(t *testing.T) {
	for _, decision := range []prompt.ApprovalDecision{prompt.APPROVAL_DENY, prompt.APPROVAL_ALLOW_ONCE} {
		t.Run(decision.String(), func(t *testing.T) {
			questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return decision }}
			config := &identityGitServer{entries: identityEntries("user.name", "Personal", "user.email", "personal@example.com")}
			s := newGitIdentityTestSession(t, questions, config)
			module := s.client("module", sessionTestModuleResult(t, "committer"), nil, "owner")

			// A module's SDK evaluates withCommit again for each field it
			// reads: that same call shares the one answer.
			first, firstErr := s.srv.AuthorizeGitIdentity(withCommitCall(t, s.ctx(module), "one"))
			again, againErr := s.srv.AuthorizeGitIdentity(withCommitCall(t, s.ctx(module), "one"))
			require.Equal(t, first, again)
			require.Equal(t, firstErr, againErr)
			require.Len(t, questions.asked(), 1)

			// Another call, or the same call from another function call, asks.
			_, _ = s.srv.AuthorizeGitIdentity(withCommitCall(t, s.ctx(module), "two"))
			other := s.client("other", sessionTestModuleResult(t, "committer"), nil, "owner")
			_, _ = s.srv.AuthorizeGitIdentity(withCommitCall(t, s.ctx(other), "one"))
			require.Len(t, questions.asked(), 3)

			// An agent in the owner's own conversation does not remember:
			// the owner's client outlives any one call.
			ctx := core.AgentToContext(s.ctx(s.owner), testAgent(t, "agent-1", "worker"))
			for range 2 {
				_, _ = s.srv.AuthorizeGitIdentity(withCommitCall(t, ctx, "one"))
			}
			require.Len(t, questions.asked(), 5)
		})
	}
}

func TestGitIdentityApprovalNotInteractive(t *testing.T) {
	config := &identityGitServer{entries: identityEntries("user.name", "Personal", "user.email", "personal@example.com")}
	s := newGitIdentityTestSession(t, nil, config)
	module := s.client("module", sessionTestModuleResult(t, "committer"), nil, "owner")
	identity, err := s.srv.AuthorizeGitIdentity(s.ctx(module))
	require.ErrorIs(t, err, core.ErrGitIdentityNotShared, "nobody can approve, so nothing is shared")
	require.ErrorIs(t, err, errApprovalNotInteractive)
	require.Zero(t, identity)
}

// failingPromptServer is an owner's client whose prompts fail, as a
// report-mode CLI's do.
type failingPromptServer struct {
	prompt.UnimplementedPromptServer
	fail func() error
}

func (p *failingPromptServer) PromptApproval(context.Context, *prompt.ApprovalRequest) (*prompt.ApprovalResponse, error) {
	return nil, p.fail()
}

func TestGitIdentityApprovalPromptFails(t *testing.T) {
	config := &identityGitServer{entries: identityEntries("user.name", "Personal", "user.email", "personal@example.com")}
	questions := &failingPromptServer{fail: func() error { return status.Error(codes.Internal, "cannot prompt in report mode") }}
	s := newGitIdentityTestSession(t, questions, config)
	module := s.client("module", sessionTestModuleResult(t, "committer"), nil, "owner")
	identity, err := s.srv.AuthorizeGitIdentity(s.ctx(module))
	require.ErrorIs(t, err, core.ErrGitIdentityNotShared, "a failed prompt is no decision, so nothing is shared")
	require.Zero(t, identity)

	// A canceled request is not a reason to commit as someone else.
	ctx, cancel := context.WithCancel(s.ctx(module))
	questions.fail = func() error { cancel(); return status.Error(codes.Canceled, "canceled") }
	_, err = s.srv.AuthorizeGitIdentity(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, core.ErrGitIdentityNotShared)
}

func TestGitIdentityBelowModule(t *testing.T) {
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return prompt.APPROVAL_ALLOW_ONCE }}
	config := &identityGitServer{entries: identityEntries("user.name", "Personal", "user.email", "personal@example.com")}
	s := newGitIdentityTestSession(t, questions, config)
	s.client("module", sessionTestModuleResult(t, "committer"), nil, "owner")

	// A dagger CLI in a container the module runs serves its own Git config:
	// the container's identity, the module's to set, needs no approval.
	box := s.client("box", dagql.ObjectResult[*core.Module]{}, nil, "owner", "module")
	boxConfig := &identityGitServer{entries: identityEntries("user.name", "Container Bot", "user.email", "bot@example.com")}
	boxConn := promptTestConn(t, func(srv *grpc.Server) { git.RegisterGitServer(srv, boxConfig) })
	s.sess.attachables.callers["box"] = &sessionAttachableCaller{ctx: t.Context(), conn: boxConn}
	identity, err := s.srv.AuthorizeGitIdentity(s.ctx(box))
	require.NoError(t, err)
	require.Equal(t, core.GitIdentity{Name: "Container Bot", Email: "bot@example.com"}, identity)
	require.Empty(t, questions.asked())
	require.Empty(t, config.paths, "the owner's config is not read")

	// One inheriting the owner's attachables is asked for like the module.
	inherited := s.client("inherited", dagql.ObjectResult[*core.Module]{}, nil, "owner", "module")
	inherited.attachablesClientID = "owner"
	identity, err = s.srv.AuthorizeGitIdentity(s.ctx(inherited))
	require.NoError(t, err)
	require.Equal(t, core.GitIdentity{Name: "Personal", Email: "personal@example.com"}, identity)
	require.Len(t, questions.asked(), 1)
}

func TestGitIdentityApprovalNothingToShare(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config *identityGitServer
	}{
		{"unconfigured", &identityGitServer{}},
		{"empty values", &identityGitServer{entries: identityEntries("user.name", "", "user.email", "")}},
		{"no git", &identityGitServer{gitErr: &git.ErrorInfo{Type: git.NOT_FOUND, Message: "git not found"}}},
		{"broken config", &identityGitServer{gitErr: &git.ErrorInfo{Type: git.CONFIG_RETRIEVAL_FAILED, Message: "broken"}}},
		{"no git service", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return prompt.APPROVAL_DENY }}
			s := newGitIdentityTestSession(t, questions, tc.config)
			module := s.client("module", sessionTestModuleResult(t, "committer"), nil, "owner")
			identity, err := s.srv.AuthorizeGitIdentity(s.ctx(module))
			require.NoError(t, err)
			require.Zero(t, identity)
			require.Empty(t, questions.asked(), "nothing to share, nothing to ask")
		})
	}
}

func TestGitIdentityApprovalConfigError(t *testing.T) {
	config := &identityGitServer{gitErr: &git.ErrorInfo{Type: git.INVALID_REQUEST, Message: "unexpected"}}
	s := newGitIdentityTestSession(t, nil, config)
	_, err := s.srv.AuthorizeGitIdentity(s.ctx(s.owner))
	require.ErrorContains(t, err, "read Git author config")
}

func TestGitIdentityApprovalPrompt(t *testing.T) {
	for _, tc := range []struct {
		identity      core.GitIdentity
		want, subject string
	}{
		{core.GitIdentity{Name: "Personal", Email: "personal@example.com"}, `Allow using your Git identity "Personal <personal@example.com>"`, `Personal <personal@example.com>`},
		{core.GitIdentity{Name: "Personal"}, `Allow using your Git identity "Personal"`, `Personal`},
		{core.GitIdentity{Email: "personal@example.com"}, `Allow using your Git identity "<personal@example.com>"`, `<personal@example.com>`},
		{core.GitIdentity{Name: "Evil\u202e\x1b[31m \"quoted\"", Email: "e@x"}, `Allow using your Git identity "Evil\u202e\x1b[31m \"quoted\" <e@x>"`, `Evil\u202e\x1b[31m \"quoted\" <e@x>`},
	} {
		spec := gitIdentityApproval(tc.identity)
		require.Equal(t, approvalSpec{Kind: approvalKindGitIdentity, Subject: tc.subject, Question: tc.want}, spec)
	}
}
