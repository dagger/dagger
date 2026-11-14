package server

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// secretTestSession is an owner whose client answers secret approvals with
// decide, and a host-less (in-engine SDK) module client it called.
type secretTestSession struct {
	*approvalTestSession
	questions *approvalPromptServer
	module    *clientRuntime
	worker    dagql.ObjectResult[*core.Agent]
}

func newSecretTestSession(t *testing.T, decide func(*prompt.ApprovalRequest) prompt.ApprovalDecision) *secretTestSession {
	t.Helper()
	questions := &approvalPromptServer{decide: decide}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	staff := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin)
	module := s.client("module", staff, &core.FunctionCall{ParentName: "Staff", Name: "gh"}, "owner")
	module.inertAttachables = true
	return &secretTestSession{
		approvalTestSession: s,
		questions:           questions,
		module:              module,
		worker:              testAgent(t, "agent-1", "worker"),
	}
}

// agentCtx is the module client's context, running on behalf of the worker.
func (s *secretTestSession) agentCtx() context.Context {
	return core.AgentToContext(s.ctx(s.module), s.worker)
}

func allowSecret(decision prompt.ApprovalDecision) func(*prompt.ApprovalRequest) prompt.ApprovalDecision {
	return func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return decision }
}

func TestSecretApprovalAgentDrivenModule(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_SESSION))
	const uri = "file:///home/me/.token"
	for range 2 {
		md, err := s.srv.AuthorizeSecret(s.agentCtx(), uri)
		require.NoError(t, err)
		require.Equal(t, "owner", md.ClientID, "an approved secret is read through the owner's host")
	}
	require.Len(t, s.questions.asked(), 1, "a session grant holds for the agent and module")
	req := s.questions.asked()[0]
	require.Equal(t, approvalKindSecret, req.Kind)
	require.Equal(t, uri, req.Subject)
	require.Equal(t, `Allow reading secret "file:///home/me/.token" (requested by agent "worker", module "staff", function "Staff.gh")?`, req.Prompt)
	require.Equal(t, `Read secret "file:///home/me/.token"?`, req.Question)
	require.False(t, req.Danger)
	require.NotEmpty(t, req.PersistentKey, "reading a secret can be allowed forever")
}

func TestSecretApprovalCommand(t *testing.T) {
	// The owner answers forever, which a command is never offered: it is
	// allowed for the session.
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_FOREVER))
	const uri = "cmd://gh auth token"
	for range 2 {
		md, err := s.srv.AuthorizeSecret(s.agentCtx(), uri)
		require.NoError(t, err)
		require.Equal(t, "owner", md.ClientID)
	}
	require.Len(t, s.questions.asked(), 1)
	req := s.questions.asked()[0]
	require.Equal(t, uri, req.Subject)
	require.Equal(t, `Allow running "gh auth token" on your machine to read a secret (requested by agent "worker", module "staff", function "Staff.gh")?`, req.Prompt)
	require.Equal(t, `Run "gh auth token" on your machine to read a secret?`, req.Question)
	require.True(t, req.Danger, "running a command on the owner's host is dangerous")
	require.Empty(t, req.PersistentKey, "running a command is never allowed forever")
}

func TestSecretApprovalDenied(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_DENY))
	for range 2 {
		md, err := s.srv.AuthorizeSecret(s.agentCtx(), "file:///home/me/.token")
		require.EqualError(t, err, `reading secret "file:///home/me/.token" was denied by the owning client`)
		require.Nil(t, md)
	}
	require.Len(t, s.questions.asked(), 2, "a denial is not remembered")
}

func TestSecretApprovalNotInteractive(t *testing.T) {
	s := newApprovalTestSession(t, func(*grpc.Server) {})
	module := s.client("module", sessionTestModuleResult(t, "staff"), nil, "owner")
	module.inertAttachables = true
	ctx := core.AgentToContext(s.ctx(module), testAgent(t, "agent-1", "worker"))
	_, err := s.srv.AuthorizeSecret(ctx, "env://TOKEN")
	require.ErrorIs(t, err, errApprovalNotInteractive)
	require.ErrorContains(t, err, `reading secret "env://TOKEN" requires approval`)
}

func TestSecretApprovalOnlyForAgents(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_SESSION))
	const call = "env://TOKEN"

	// Without an agent, a host-less module reads as itself, and is denied
	// when it reads, as before.
	md, err := s.srv.AuthorizeSecret(s.ctx(s.module), call)
	require.NoError(t, err)
	require.Nil(t, md, "the caller reads its own secret")

	// A module client with a host of its own (its runtime container's) reads
	// from it, agent or not.
	other := s.client("container-module", sessionTestModuleResult(t, "other"), nil, "owner")
	md, err = s.srv.AuthorizeSecret(core.AgentToContext(s.ctx(other), s.worker), call)
	require.NoError(t, err)
	require.Nil(t, md)

	// The owner reads its own secrets, in its agents' tools too.
	md, err = s.srv.AuthorizeSecret(core.AgentToContext(s.ctx(s.owner), s.worker), call)
	require.NoError(t, err)
	require.Nil(t, md)

	// Workspace routing reads as the workspace's owner, its own secret.
	routed := engine.ContextWithClientMetadata(s.agentCtx(), s.owner.clientMetadata)
	md, err = s.srv.AuthorizeSecret(routed, "env://ROUTED")
	require.NoError(t, err)
	require.Nil(t, md)

	require.Empty(t, s.questions.asked())
}

func TestSecretApprovalPerModule(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_SESSION))
	const uri = "file:///home/me/.token"
	_, err := s.srv.AuthorizeSecret(s.agentCtx(), uri)
	require.NoError(t, err)
	// Grants are scoped to the module: another agent it drives shares one.
	otherAgent := core.AgentToContext(s.ctx(s.module), testAgent(t, "agent-2", "worker"))
	_, err = s.srv.AuthorizeSecret(otherAgent, uri)
	require.NoError(t, err)
	require.Len(t, s.questions.asked(), 1)
	// Another secret asks again.
	_, err = s.srv.AuthorizeSecret(s.agentCtx(), "file:///home/me/.other")
	require.NoError(t, err)
	require.Len(t, s.questions.asked(), 2)
}

func TestSecretApprovalInertClient(t *testing.T) {
	// An agent's snippet evaluated by an in-engine SDK client serving no
	// module (DangEval without a module): host-less, so its owner, the client
	// that started it, is asked, rather than the client itself.
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_SESSION))
	eval := s.client("eval", dagql.ObjectResult[*core.Module]{}, nil, "owner")
	eval.inertAttachables = true
	// Its nested clients inherit inertness (see hostFromParent).
	nested := s.client("eval-nested", dagql.ObjectResult[*core.Module]{}, nil, "owner", "eval")
	nested.inertAttachables = true
	const uri = "env://SHELL"
	for _, client := range []*clientRuntime{eval, nested} {
		md, err := s.srv.AuthorizeSecret(core.AgentToContext(s.ctx(client), s.worker), uri)
		require.NoError(t, err)
		require.Equal(t, "owner", md.ClientID, "an approved secret is read through the owner's host")
	}
	require.Len(t, s.questions.asked(), 1, "a session grant holds for the agent's snippets")
	req := s.questions.asked()[0]
	require.Equal(t, `Allow reading secret "env://SHELL" (requested by agent "worker")?`, req.Prompt)
	require.Empty(t, req.PersistentKey, "nothing to remember forever without a module")

	// Without an agent it reads as itself, and is denied when it reads.
	md, err := s.srv.AuthorizeSecret(s.ctx(eval), uri)
	require.NoError(t, err)
	require.Nil(t, md)
	require.Len(t, s.questions.asked(), 1)
}

func TestSecretApprovalInertRoot(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_SESSION))
	root := s.client("inert-root", dagql.ObjectResult[*core.Module]{}, nil)
	root.inertAttachables = true
	_, err := s.srv.AuthorizeSecret(core.AgentToContext(s.ctx(root), s.worker), "env://SHELL")
	require.ErrorContains(t, err, "no owning client")
	require.Empty(t, s.questions.asked())
}

func TestGitPushOwnerBoundaries(t *testing.T) {
	s := newApprovalTestSession(t, func(*grpc.Server) {})
	inert := func(c *clientRuntime) *clientRuntime {
		c.inertAttachables = true
		return c
	}
	none := dagql.ObjectResult[*core.Module]{}
	mod := sessionTestModuleResult(t, "mod")
	for _, tc := range []struct {
		name      string
		client    *clientRuntime
		owner     string
		delegated bool
		err       string
	}{
		{"the root owns itself", s.owner, "owner", false, ""},
		{"a non-module client owns itself",
			s.client("cli", none, nil, "owner"), "cli", false, ""},
		{"a module asks its caller",
			s.client("module", mod, nil, "owner"), "owner", true, ""},
		{"a module's non-module client asks the module's caller",
			s.client("module-nested", none, nil, "owner", "module"), "owner", true, ""},
		{"an inert client asks its caller",
			inert(s.client("eval", none, nil, "owner")), "owner", true, ""},
		{"an inert client's nested client asks the inert client's caller",
			inert(s.client("eval-nested", none, nil, "owner", "eval")), "owner", true, ""},
		{"an inert client under a non-module client asks that client",
			inert(s.client("cli-eval", none, nil, "owner", "cli")), "cli", true, ""},
		{"a module above an inert client is the boundary",
			inert(s.client("module-eval", none, nil, "owner", "module")), "owner", true, ""},
		{"an inert module asks its caller",
			inert(s.client("inert-module", mod, nil, "owner")), "owner", true, ""},
		{"an inert root has no owner",
			inert(s.client("inert-root", none, nil)), "", true, "no owning client"},
		{"a client under an inert root has no owner",
			s.client("under-inert-root", none, nil, "inert-root"), "", true, "no owning client"},
		{"a module root has no owner",
			s.client("module-root", mod, nil), "", true, "no owning client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, delegated, err := gitPushOwner(tc.client)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				require.Nil(t, owner)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.owner, owner.clientID)
			require.Equal(t, tc.delegated, delegated)
		})
	}
}

func TestSecretApprovalPrompt(t *testing.T) {
	spec := secretApproval("file:///x\u202e\x1b[31m")
	require.Equal(t, `file:///x\u202e\x1b[31m`, spec.Subject)
	require.Equal(t, `Allow reading secret "file:///x\u202e\x1b[31m"`, spec.Question)
	require.Equal(t, `Read secret "file:///x\u202e\x1b[31m"`, spec.Action)
	spec = secretApproval("cmd://echo \u202e\x1b[31m")
	require.Equal(t, `cmd://echo \u202e\x1b[31m`, spec.Subject)
	require.Equal(t, `Allow running "echo \u202e\x1b[31m" on your machine to read a secret`, spec.Question)
	require.Equal(t, `Run "echo \u202e\x1b[31m" on your machine to read a secret`, spec.Action)
}

// restoredLoadCtxs are a restored agent's binding loads, each in a host-less
// client of its own (see Server.WithHostlessClient): under the owner, as when
// the CLI that restored a chief drives its loop, and under a module client, as
// when staff's sendTo drives a worker's after a resume.
func (s *secretTestSession) restoredLoadCtxs() []context.Context {
	none := dagql.ObjectResult[*core.Module]{}
	underOwner := s.client("restored-under-owner", none, nil, "owner")
	underOwner.inertAttachables = true
	underModule := s.client("restored-under-module", none, nil, "owner", "module")
	underModule.inertAttachables = true
	var ctxs []context.Context
	for _, client := range []*clientRuntime{underOwner, underModule} {
		ctxs = append(ctxs, core.ContextWithRestoredBindingLoad(core.AgentToContext(s.ctx(client), s.worker)))
	}
	return ctxs
}

func TestSecretApprovalRestoredBinding(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_ONCE))
	// Whichever client drives the loop, a restored binding's secret asks the
	// owner, naming the agent alone, whoever minted it in the recorded
	// session, and is never allowed forever.
	asked := 0
	for _, ctx := range s.restoredLoadCtxs() {
		for _, uri := range []string{"env://OWNERS", "file:///home/me/.token", "cmd://gh auth token", "cmd://gh auth token"} {
			md, err := s.srv.AuthorizeSecret(ctx, uri)
			require.NoError(t, err)
			require.Equal(t, "owner", md.ClientID, "an approved secret is read through the owner's host, once")
			asked++
			require.Len(t, s.questions.asked(), asked, "allowed once, so asked every time")
			req := s.questions.asked()[asked-1]
			require.Empty(t, req.PersistentKey, "a restored read is never allowed forever")
			require.Equal(t, &prompt.ApprovalRequester{AgentName: "worker", AgentHandle: "agent-1"}, req.Requester,
				"the module driving the loop did not build the recipe")
		}
	}
	req := s.questions.asked()[2]
	require.Equal(t, approvalKindSecret, req.Kind)
	require.Equal(t, `Allow running "gh auth token" on your machine to read a secret (requested by agent "worker")?`, req.Prompt)
	require.Equal(t, `Run "gh auth token" on your machine to read a secret?`, req.Question)
	require.True(t, req.Danger)

	// The same read by a fresh agent's host-less module asks as before.
	md, err := s.srv.AuthorizeSecret(s.agentCtx(), "env://OWNERS")
	require.NoError(t, err)
	require.Equal(t, "owner", md.ClientID)
	require.NotEmpty(t, s.questions.asked()[asked].PersistentKey, "outside a resume, forever is offered")
}

func TestSecretApprovalRestoredBindingSession(t *testing.T) {
	// The owner may allow a read for the rest of its own session: that is a
	// grant of the new session, not one carried over from the recorded one.
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_FOREVER))
	for _, ctx := range s.restoredLoadCtxs() {
		md, err := s.srv.AuthorizeSecret(ctx, "env://OWNERS")
		require.NoError(t, err)
		require.Equal(t, "owner", md.ClientID)
	}
	require.Len(t, s.questions.asked(), 1, "forever is not offered, so it holds for the session")
}

func TestSecretApprovalRestoredBindingDenied(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_DENY))
	md, err := s.srv.AuthorizeSecret(s.restoredLoadCtxs()[0], "file:///home/me/.token")
	require.EqualError(t, err, `reading secret "file:///home/me/.token" for a restored agent was denied by the owning client`)
	require.Nil(t, md)
}

func TestSecretApprovalRestoredBindingNotInteractive(t *testing.T) {
	s := newApprovalTestSession(t, func(*grpc.Server) {})
	restored := s.client("restored", dagql.ObjectResult[*core.Module]{}, nil, "owner")
	restored.inertAttachables = true
	ctx := core.ContextWithRestoredBindingLoad(core.AgentToContext(s.ctx(restored), testAgent(t, "agent-1", "worker")))
	_, err := s.srv.AuthorizeSecret(ctx, "file:///home/me/.token")
	require.ErrorIs(t, err, errApprovalNotInteractive)
	require.ErrorContains(t, err, `reading secret "file:///home/me/.token" for a restored agent requires approval`)
}
