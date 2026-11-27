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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const dockerSock = "/var/run/docker.sock"

func TestSocketApprovalAgentDrivenModule(t *testing.T) {
	// The owner answers forever, which a socket is never offered: it is
	// allowed for the session.
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_FOREVER))
	for range 2 {
		md, err := s.srv.AuthorizeHostSocket(s.agentCtx(), dockerSock)
		require.NoError(t, err)
		require.Equal(t, "owner", md.ClientID, "an approved socket is forwarded from the owner's host")
	}
	require.Len(t, s.questions.asked(), 1, "a session grant holds for the agent and module")
	req := s.questions.asked()[0]
	require.Equal(t, approvalKindSocket, req.Kind)
	require.Equal(t, dockerSock, req.Subject)
	require.Equal(t, `Allow access to socket "/var/run/docker.sock" on your machine (requested by agent "worker", module "staff", function "Staff.gh")?`, req.Prompt)
	require.Equal(t, `Access socket "/var/run/docker.sock" on your machine?`, req.Question)
	require.True(t, req.Danger, "a host socket can be root on the owner's host")
	require.Empty(t, req.PersistentKey, "a host socket is never allowed forever")

	// Another socket asks again.
	_, err := s.srv.AuthorizeHostSocket(s.agentCtx(), "/run/user/1000/podman.sock")
	require.NoError(t, err)
	require.Len(t, s.questions.asked(), 2)
}

func TestSocketApprovalDenied(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_DENY))
	for range 2 {
		md, err := s.srv.AuthorizeHostSocket(s.agentCtx(), dockerSock)
		require.EqualError(t, err, `access to socket "/var/run/docker.sock" on your machine was denied by the owning client`)
		require.Nil(t, md)
	}
	require.Len(t, s.questions.asked(), 2, "a denial is not remembered")
}

func TestSocketApprovalNotInteractive(t *testing.T) {
	s := newApprovalTestSession(t, func(*grpc.Server) {})
	module := inert(s.client("module", sessionTestModuleResult(t, "staff"), nil, "owner"))
	ctx := core.AgentToContext(s.ctx(module), testAgent(t, "agent-1", "worker"))
	_, err := s.srv.AuthorizeHostSocket(ctx, dockerSock)
	require.ErrorIs(t, err, errApprovalNotInteractive)
	require.ErrorContains(t, err, `access to socket "/var/run/docker.sock" on your machine requires approval`)
}

func TestSocketApprovalWithoutAgent(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_SESSION))

	// Without an agent, a client without a host of its own is refused at
	// once, rather than minting a socket that only fails when used.
	eval := inert(s.client("eval", dagql.ObjectResult[*core.Module]{}, nil, "owner"))
	for _, client := range []*clientRuntime{s.module, eval} {
		md, err := s.srv.AuthorizeHostSocket(s.ctx(client), dockerSock)
		require.ErrorContains(t, err, `access to socket "/var/run/docker.sock" on your machine is denied without an agent's request`)
		require.Equal(t, codes.PermissionDenied, status.Code(err))
		require.Nil(t, md)
	}

	// A client with a host of its own forwards its own socket, agent or not.
	other := s.client("container-module", sessionTestModuleResult(t, "other"), nil, "owner")
	for _, ctx := range []context.Context{
		core.AgentToContext(s.ctx(other), s.worker),
		core.AgentToContext(s.ctx(s.owner), s.worker),
		s.ctx(s.owner),
		// Workspace routing forwards as the workspace's owner, its own.
		engine.ContextWithClientMetadata(s.agentCtx(), s.owner.clientMetadata),
	} {
		md, err := s.srv.AuthorizeHostSocket(ctx, dockerSock)
		require.NoError(t, err)
		require.Nil(t, md)
	}
	require.Empty(t, s.questions.asked())
}

func TestSocketApprovalInertClient(t *testing.T) {
	// An agent's snippet (DangEval without a module) and its nested
	// clients ask the client that started it.
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_SESSION))
	eval := inert(s.client("eval", dagql.ObjectResult[*core.Module]{}, nil, "owner"))
	nested := inert(s.client("eval-nested", dagql.ObjectResult[*core.Module]{}, nil, "owner", "eval"))
	for _, client := range []*clientRuntime{eval, nested} {
		md, err := s.srv.AuthorizeHostSocket(core.AgentToContext(s.ctx(client), s.worker), dockerSock)
		require.NoError(t, err)
		require.Equal(t, "owner", md.ClientID)
	}
	require.Len(t, s.questions.asked(), 1)
	require.Equal(t, `Allow access to socket "/var/run/docker.sock" on your machine (requested by agent "worker")?`, s.questions.asked()[0].Prompt)
}

func TestSocketApprovalRestoredBinding(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_ONCE))
	for i, ctx := range s.restoredLoadCtxs() {
		md, err := s.srv.AuthorizeHostSocket(ctx, dockerSock)
		require.NoError(t, err)
		require.Equal(t, "owner", md.ClientID)
		require.Len(t, s.questions.asked(), i+1, "allowed once, so asked every time")
		req := s.questions.asked()[i]
		require.Empty(t, req.PersistentKey)
		require.Equal(t, &prompt.ApprovalRequester{AgentName: "worker", AgentHandle: "agent-1"}, req.Requester,
			"the module driving the loop did not build the recipe")
	}

	denied := newSecretTestSession(t, allowSecret(prompt.APPROVAL_DENY))
	_, err := denied.srv.AuthorizeHostSocket(denied.restoredLoadCtxs()[0], dockerSock)
	require.EqualError(t, err, `access to socket "/var/run/docker.sock" on your machine for a restored agent was denied by the owning client`)
}

func TestSocketApprovalPrompt(t *testing.T) {
	spec := socketApproval("/tmp/x\u202e\x1b[31m.sock")
	require.Equal(t, `/tmp/x\u202e\x1b[31m.sock`, spec.Subject)
	require.Equal(t, `Allow access to socket "/tmp/x\u202e\x1b[31m.sock" on your machine`, spec.Question)
	require.Equal(t, `Access socket "/tmp/x\u202e\x1b[31m.sock" on your machine`, spec.Action)
	require.True(t, spec.Danger)
	require.True(t, spec.NoForever)
}

func TestHostNetworkApproval(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_FOREVER))
	for range 2 {
		md, err := s.srv.AuthorizeHostNetwork(s.agentCtx(), "localhost:6060")
		require.NoError(t, err)
		require.Equal(t, "owner", md.ClientID, "an approved tunnel goes through the owner's machine")
	}
	require.Len(t, s.questions.asked(), 1, "forever is not offered, so it holds for the session")
	req := s.questions.asked()[0]
	require.Equal(t, approvalKindHostNetwork, req.Kind)
	require.Equal(t, "localhost:6060", req.Subject)
	require.Equal(t, `Allow connecting to localhost:6060 on your machine (requested by agent "worker", module "staff", function "Staff.gh")?`, req.Prompt)
	require.Equal(t, `Connect to localhost:6060 on your machine?`, req.Question)
	require.Empty(t, req.PersistentKey, "a host port is never allowed forever")
	require.False(t, req.Danger)

	// Another port asks again.
	_, err := s.srv.AuthorizeHostNetwork(s.agentCtx(), "localhost:5432")
	require.NoError(t, err)
	require.Len(t, s.questions.asked(), 2)

	// Without an agent: refused. With a host of its own: its own network.
	_, err = s.srv.AuthorizeHostNetwork(s.ctx(s.module), "localhost:6060")
	require.ErrorContains(t, err, `connecting to localhost:6060 on your machine is denied without an agent's request`)
	md, err := s.srv.AuthorizeHostNetwork(s.ctx(s.owner), "localhost:6060")
	require.NoError(t, err)
	require.Nil(t, md)

	denied := newSecretTestSession(t, allowSecret(prompt.APPROVAL_DENY))
	_, err = denied.srv.AuthorizeHostNetwork(denied.agentCtx(), "localhost:6060")
	require.EqualError(t, err, `connecting to localhost:6060 on your machine was denied by the owning client`)

	quiet := newApprovalTestSession(t, func(*grpc.Server) {})
	module := inert(quiet.client("module", sessionTestModuleResult(t, "staff"), nil, "owner"))
	_, err = quiet.srv.AuthorizeHostNetwork(core.AgentToContext(quiet.ctx(module), testAgent(t, "agent-1", "worker")), "localhost:6060")
	require.ErrorIs(t, err, errApprovalNotInteractive)
	require.ErrorContains(t, err, `connecting to localhost:6060 on your machine requires approval`)
}

func TestHostNetworkApprovalPrompt(t *testing.T) {
	spec := hostNetworkApproval("x\u202e\x1b[31m:1")
	require.Equal(t, `x\u202e\x1b[31m:1`, spec.Subject)
	require.Equal(t, `Allow connecting to x\u202e\x1b[31m:1 on your machine`, spec.Question)
	require.True(t, spec.NoForever)
}

func TestSSHAgentApproval(t *testing.T) {
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_SESSION))
	const remote = "ssh://git@github.com/org/repo.git"
	md, err := s.srv.AuthorizeSSHAgent(s.agentCtx(), remote)
	require.NoError(t, err)
	require.Equal(t, "owner", md.ClientID, "the owner lends its SSH agent")
	require.Len(t, s.questions.asked(), 1)
	req := s.questions.asked()[0]
	require.Equal(t, approvalKindSSHAgent, req.Kind)
	require.Equal(t, sshAgentApprovalSubject, req.Subject)
	require.Equal(t, `Allow using your SSH keys to authenticate to github.com (ssh://git@github.com/org/repo.git) (requested by agent "worker", module "staff", function "Staff.gh")?`, req.Prompt)
	require.Equal(t, `Use your SSH keys to authenticate to github.com (ssh://git@github.com/org/repo.git)?`, req.Question)
	require.NotEmpty(t, req.PersistentKey, "a fresh agent's module may be allowed forever")

	// A forwarded agent signs for any host: the grant is the owner's
	// agent, not one remote.
	for _, other := range []string{"git@gitlab.com:org/other.git", ""} {
		md, err := s.srv.AuthorizeSSHAgent(s.agentCtx(), other)
		require.NoError(t, err)
		require.Equal(t, "owner", md.ClientID)
	}
	require.Len(t, s.questions.asked(), 1)
}

func TestSSHAgentApprovalPrompt(t *testing.T) {
	spec := sshAgentApproval("")
	require.Equal(t, "Allow using your SSH keys", spec.Question)
	require.Equal(t, "Use your SSH keys", spec.Action)
	spec = sshAgentApproval("git@github.com:org/\x1b[31mrepo.git")
	require.Equal(t, `Allow using your SSH keys to authenticate to github.com (git@github.com:org/\x1b[31mrepo.git)`, spec.Question)
	require.Equal(t, sshAgentApprovalSubject, spec.Subject)
	require.False(t, spec.Danger)
}

func TestSSHAgentApprovalCauses(t *testing.T) {
	const remote = "ssh://git@github.com/org/repo.git"

	// Without an agent: refused, naming why.
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_SESSION))
	_, err := s.srv.AuthorizeSSHAgent(s.ctx(s.module), remote)
	require.ErrorContains(t, err, `using your SSH keys for "ssh://git@github.com/org/repo.git" is denied without an agent's request`)
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	// A client with a host of its own uses its own, as always.
	md, err := s.srv.AuthorizeSSHAgent(s.ctx(s.owner), remote)
	require.NoError(t, err)
	require.Nil(t, md)
	require.Empty(t, s.questions.asked())

	// Denied.
	denied := newSecretTestSession(t, allowSecret(prompt.APPROVAL_DENY))
	_, err = denied.srv.AuthorizeSSHAgent(denied.agentCtx(), remote)
	require.EqualError(t, err, `using your SSH keys for "ssh://git@github.com/org/repo.git" was denied by the owning client`)

	// The owner cannot prompt.
	quiet := newApprovalTestSession(t, func(*grpc.Server) {})
	module := inert(quiet.client("module", sessionTestModuleResult(t, "staff"), nil, "owner"))
	_, err = quiet.srv.AuthorizeSSHAgent(core.AgentToContext(quiet.ctx(module), testAgent(t, "agent-1", "worker")), "")
	require.ErrorIs(t, err, errApprovalNotInteractive)
	require.ErrorContains(t, err, `using your SSH keys requires approval`)
}

func TestSSHAgentApprovalRestoredBinding(t *testing.T) {
	// A resumed agent's recipe scoping the owner's SSH agent asks again,
	// never forever, naming the agent alone.
	s := newSecretTestSession(t, allowSecret(prompt.APPROVAL_ALLOW_FOREVER))
	for _, ctx := range s.restoredLoadCtxs() {
		md, err := s.srv.AuthorizeSSHAgent(ctx, "ssh://git@github.com/org/repo.git")
		require.NoError(t, err)
		require.Equal(t, "owner", md.ClientID)
	}
	require.Len(t, s.questions.asked(), 1, "forever is not offered, so it holds for the session")
	req := s.questions.asked()[0]
	require.Empty(t, req.PersistentKey)
	require.Equal(t, &prompt.ApprovalRequester{AgentName: "worker", AgentHandle: "agent-1"}, req.Requester)
}
