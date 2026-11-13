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
	const uri = "cmd://gh auth token"
	for range 2 {
		md, err := s.srv.AuthorizeSecret(s.agentCtx(), uri)
		require.NoError(t, err)
		require.Equal(t, "owner", md.ClientID, "an approved secret is read through the owner's host")
	}
	require.Len(t, s.questions.asked(), 1, "a session grant holds for the agent and module")
	req := s.questions.asked()[0]
	require.Equal(t, approvalKindSecret, req.Kind)
	require.Equal(t, uri, req.Subject)
	require.Equal(t, `Allow reading secret "cmd://gh auth token" (requested by agent "worker", module "staff", function "Staff.gh")?`, req.Prompt)
	require.False(t, req.Danger)
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
	require.Equal(t, "module", md.ClientID)

	// A module client with a host of its own (its runtime container's) reads
	// from it, agent or not.
	other := s.client("container-module", sessionTestModuleResult(t, "other"), nil, "owner")
	md, err = s.srv.AuthorizeSecret(core.AgentToContext(s.ctx(other), s.worker), call)
	require.NoError(t, err)
	require.Equal(t, "container-module", md.ClientID)

	// The owner reads its own secrets, in its agents' tools too.
	md, err = s.srv.AuthorizeSecret(core.AgentToContext(s.ctx(s.owner), s.worker), call)
	require.NoError(t, err)
	require.Equal(t, "owner", md.ClientID)

	// Workspace routing reads as the workspace's owner.
	routed := engine.ContextWithClientMetadata(s.agentCtx(), s.owner.clientMetadata)
	md, err = s.srv.AuthorizeSecret(routed, "env://ROUTED")
	require.NoError(t, err)
	require.Equal(t, "owner", md.ClientID)

	require.Empty(t, s.questions.asked())
}

func TestSecretApprovalPerAgent(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_SESSION))
	const uri = "file:///home/me/.token"
	_, err := s.srv.AuthorizeSecret(s.agentCtx(), uri)
	require.NoError(t, err)
	// Another agent the same module drives, or another secret, asks again.
	otherAgent := core.AgentToContext(s.ctx(s.module), testAgent(t, "agent-2", "worker"))
	_, err = s.srv.AuthorizeSecret(otherAgent, uri)
	require.NoError(t, err)
	_, err = s.srv.AuthorizeSecret(s.agentCtx(), "file:///home/me/.other")
	require.NoError(t, err)
	require.Len(t, s.questions.asked(), 3)
}

func TestSecretApprovalPrompt(t *testing.T) {
	spec := secretApproval("cmd://echo \u202e\x1b[31m")
	require.Equal(t, `cmd://echo \u202e\x1b[31m`, spec.Subject)
	require.Equal(t, `Allow reading secret "cmd://echo \u202e\x1b[31m"`, spec.Question)
}
