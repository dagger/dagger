package server

import (
	"context"
	"fmt"
	"strconv"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
)

const approvalKindSecret = "secret"

// secretApproval asks the owner to let an agent's module read a secret from
// the owner's host. The URI is shown verbatim, escaped for terminals
// (including bidi/control characters): a cmd:// URI runs a command on the
// owner's host, which the owner must see to decide.
func secretApproval(uri string) approvalSpec {
	return approvalSpec{
		Kind:     approvalKindSecret,
		Subject:  literalText(uri),
		Question: "Allow reading secret " + strconv.QuoteToASCII(uri),
	}
}

// AuthorizeSecret returns the trusted owner whose host Query.secret(uri:)
// reads its secret from, or nil when the calling client reads it through its
// own attachables, as it always did.
//
// A client without a host of its own (an in-engine SDK client, see
// resolveClientAttachableCaller) could only ever be denied, and still is when
// no agent is involved: a module function reads no host secret it was not
// passed. When it runs on behalf of an agent, though, the secret is the
// owner's to grant: the trusted caller before the first module boundary (see
// gitPushOwner) is asked, naming the agent, module and function that asks
// (see requestApproval), and on approval the secret is read from the owner's
// host, once (see Query.secret). A denial, or an owner that cannot prompt,
// fails the call.
//
// The agent only scopes the request, it does not vouch for it: any host-less
// module can run an agent of its own (spawn a worker over its tools) and so
// ask the owner for any URI. The prompt names that module, and the agent and
// function, so the owner sees who asks.
//
// Secrets the owner declared itself never get here: workspace settings and
// workspace artifacts (e.g. a module constructor's `cmd://gh auth token`
// setting) are resolved with the owner's own client metadata, so they read
// through the owner as before.
func (srv *Server) AuthorizeSecret(ctx context.Context, uri string) (*engine.ClientMetadata, error) {
	// The client whose metadata the call runs with, validated against the
	// held scope: a workspace owner's routing reads as that owner.
	client, err := srv.workspaceRuntimeFromContext(ctx)
	if err != nil {
		// Not a client this gate can place: read as the caller, exactly as
		// without the gate. The gate only ever widens a host-less client's
		// access, never narrows anyone's.
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
		return nil, nil
	}
	quoted := strconv.QuoteToASCII(uri)
	owner, _, err := gitPushOwner(client)
	if err != nil {
		return nil, fmt.Errorf("reading secret %s: %w", quoted, err)
	}
	ownerMD, err := sess.clientMetadataSnapshot(owner.clientRecord)
	if err != nil {
		return nil, err
	}
	allowed, err := srv.requestApproval(ctx, client, owner, secretApproval(uri))
	if err != nil {
		return nil, fmt.Errorf("reading secret %s requires approval: %w", quoted, err)
	}
	if !allowed {
		return nil, fmt.Errorf("reading secret %s was denied by the owning client", quoted)
	}
	markOwnerSecretRead(client)
	return ownerMD, nil
}

// markOwnerSecretRead marks the function calls client and its ancestors are
// running as having read a secret through their owner's host (see
// core.FunctionCall.MarkOwnerSecretRead): their results are derived from it,
// with a grant for one agent and module, so none of them may be reused.
func markOwnerSecretRead(client *clientRuntime) {
	client.fnCall.MarkOwnerSecretRead()
	parents, err := client.daggerSession.ancestorRuntimes(client.clientRecord)
	if err != nil {
		return
	}
	for _, parent := range parents {
		parent.fnCall.MarkOwnerSecretRead()
	}
}
