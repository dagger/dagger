package server

import (
	"context"
	"fmt"
	"strconv"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	approvalKindSocket      = "socket"
	approvalKindHostNetwork = "host-network"
	approvalKindSSHAgent    = "ssh-agent"

	// sshAgentApprovalSubject is the one subject an ssh-agent grant is keyed
	// by: a forwarded agent signs for any host it is asked to, so a grant
	// covers the owner's SSH agent, whatever remote it was first asked for.
	sshAgentApprovalSubject = "SSH agent"
)

// errHostAccessDenied is how a client without a host of its own is refused
// its caller's host when no agent asks for it: as the session refuses its
// attachables (see resolveClientAttachableCaller).
var errHostAccessDenied = status.Error(codes.PermissionDenied, "SDK client access to host session attachables is denied")

// socketApproval asks the owner to let an agent use a Unix socket on the
// owner's host. A socket such as /var/run/docker.sock is root on that host,
// so the request is dangerous, and never allowed forever. The path is shown
// verbatim, escaped for terminals (including bidi/control characters).
func socketApproval(path string) approvalSpec {
	return approvalSpec{
		Kind:      approvalKindSocket,
		Subject:   literalText(path),
		Question:  "Allow access to socket " + strconv.QuoteToASCII(path) + " on your machine",
		Action:    "Access socket " + strconv.QuoteToASCII(path) + " on your machine",
		Danger:    true,
		NoForever: true,
	}
}

// hostNetworkApproval asks the owner to let an agent connect to hostPort
// ("localhost:6060") from the owner's machine, tunneled through the owner
// (Host.service): e.g. a database listening on the owner's localhost. Never
// allowed forever. The address is shown verbatim, escaped for terminals.
func hostNetworkApproval(hostPort string) approvalSpec {
	return approvalSpec{
		Kind:      approvalKindHostNetwork,
		Subject:   literalText(hostPort),
		Question:  "Allow connecting to " + literalText(hostPort) + " on your machine",
		Action:    "Connect to " + literalText(hostPort) + " on your machine",
		NoForever: true,
	}
}

// sshAgentApproval asks the owner to let an agent authenticate with the
// owner's SSH keys, naming the remote it authenticates to when it is known.
// The grant is one per owner (see sshAgentApprovalSubject), not per remote.
func sshAgentApproval(remote string) approvalSpec {
	what := "your SSH keys"
	if remote != "" {
		host := remote
		if u, err := gitutil.ParseURL(remote); err == nil && u.Host != "" {
			host = u.Host
		}
		what += " to authenticate to " + literalText(host) + " (" + literalText(remote) + ")"
	}
	return approvalSpec{
		Kind:     approvalKindSSHAgent,
		Subject:  sshAgentApprovalSubject,
		Question: "Allow using " + what,
		Action:   "Use " + what,
	}
}

// sshAgentAccess names a use of the owner's SSH agent in errors.
func sshAgentAccess(remote string) string {
	if remote == "" {
		return "using your SSH keys"
	}
	return "using your SSH keys for " + strconv.QuoteToASCII(remote)
}

// AuthorizeHostSocket returns the trusted owner whose host Host.unixSocket
// forwards path from, or nil when the calling client forwards its own host's
// socket, as it always did.
//
// A client without a host of its own (an in-engine SDK client, see
// resolveClientAttachableCaller) is refused outright when no agent is
// involved: a module function forwards no host socket it was not passed. When
// it runs on behalf of an agent, as an agent's DangEval snippet does, the
// socket is the owner's to grant, and the owner is asked (see gitPushOwner and
// requestApproval). Such a socket (/var/run/docker.sock, say) is root on the
// owner's host: the request is dangerous and never allowed forever. On
// approval, the socket is forwarded from the owner's host. A denial, or an
// owner that cannot prompt, fails the call.
//
// A restored agent's bindings load in a fresh host-less client (see
// core.LoadBoundObject), so they take this path too: nothing gains access by
// being resumed, and the prompt names the agent alone.
func (srv *Server) AuthorizeHostSocket(ctx context.Context, path string) (*engine.ClientMetadata, error) {
	return srv.authorizeOwnerHostAccess(ctx,
		"access to socket "+strconv.QuoteToASCII(path)+" on your machine",
		socketApproval(path))
}

// AuthorizeHostNetwork returns the trusted owner whose machine Host.service
// tunnels connections to hostPort ("localhost:6060") through, or nil when the
// calling client tunnels through its own host, as it always did. As for
// AuthorizeHostSocket, a client without a host of its own is refused without
// an agent, and asks its owner on an agent's behalf.
func (srv *Server) AuthorizeHostNetwork(ctx context.Context, hostPort string) (*engine.ClientMetadata, error) {
	return srv.authorizeOwnerHostAccess(ctx,
		"connecting to "+literalText(hostPort)+" on your machine",
		hostNetworkApproval(hostPort))
}

// AuthorizeSSHAgent returns the trusted owner whose SSH agent Host.
// _sshAuthSocket may use for a caller without one of its own, asking that
// owner first, or nil when the calling client has a host of its own (it uses
// its own SSH agent or none, as it always did). remote is the remote the
// agent authenticates to, when known, for the question.
//
// As for AuthorizeHostSocket, a client without a host of its own is refused
// when no agent is involved, and asks its owner on an agent's behalf. This is
// how a resumed agent's recipe that scoped the owner's SSH agent (e.g. a
// workspace snapshot of an SSH checkout) loads again: its host-less restored
// load asks, never allowed forever, instead of failing.
func (srv *Server) AuthorizeSSHAgent(ctx context.Context, remote string) (*engine.ClientMetadata, error) {
	return srv.authorizeOwnerHostAccess(ctx, sshAgentAccess(remote), sshAgentApproval(remote))
}

// authorizeOwnerHostAccess is the owner's approval of what (as errors name
// it) for a client without a host of its own on an agent's behalf, as
// AuthorizeSecret asks: the owner's metadata once it approved, or nil when
// the caller has a host of its own.
func (srv *Server) authorizeOwnerHostAccess(ctx context.Context, what string, spec approvalSpec) (*engine.ClientMetadata, error) {
	client, err := srv.workspaceRuntimeFromContext(ctx)
	if err != nil {
		// Not a client this gate can place: as the caller, exactly as
		// without the gate.
		return nil, nil //nolint:nilerr // see above
	}
	sess := client.daggerSession
	sess.scopeMu.Lock()
	inert := client.inertAttachables
	sess.scopeMu.Unlock()
	if !inert {
		return nil, nil
	}
	if _, ok := core.CallerAgent(ctx); !ok {
		return nil, fmt.Errorf("%s is denied without an agent's request: %w", what, errHostAccessDenied)
	}
	owner, _, err := gitPushOwner(client)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	ownerMD, err := sess.clientMetadataSnapshot(owner.clientRecord)
	if err != nil {
		return nil, err
	}
	requester := client
	if core.IsRestoredBindingLoad(ctx) {
		what += " for a restored agent"
		spec.NoForever = true
		requester = owner
	}
	allowed, err := srv.requestApproval(ctx, requester, owner, spec)
	if err != nil {
		return nil, fmt.Errorf("%s requires approval: %w", what, err)
	}
	if !allowed {
		return nil, fmt.Errorf("%s was denied by the owning client", what)
	}
	// What the calls running here return may be derived from the owner's
	// host, with a grant for one agent and module: none may be reused.
	markOwnerSecretRead(client)
	return ownerMD, nil
}
