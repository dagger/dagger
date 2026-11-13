package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/session/git"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const approvalKindGitIdentity = "git-identity"

// gitIdentityPromptPrefix starts every Git identity approval prompt.
const gitIdentityPromptPrefix = "Allow using your Git identity "

// AuthorizeGitIdentity returns the owner's Git identity (user.name and
// user.email at its working directory), to attribute a commit made without an
// explicit author.
//
// The owner is the trusted caller before the first module boundary, as for
// push: module clients hold no host attachables of their own, and a module
// cannot regain its owner's by spawning a nested client. A direct call from
// that owner shares its identity as before. When an agent asks (a tool in the
// owner's own conversation, or a module function an agent called) or a module
// does, the identity is the owner's to share: the engine reads it first, so
// the prompt can show exactly what is shared, and only returns it once the
// owner approves (see requestApproval). A grant is the agent's and module's,
// across its functions, for that identity (see gitIdentityApproval), once,
// for the session or forever.
//
// A client serving its own attachables below a module boundary (a dagger CLI
// in a container a module runs) has its own Git config, the module's to set:
// that identity is returned as is, as it was before approvals.
//
// An unconfigured identity has nothing to share and is returned empty without
// asking. When the owner denies, or no answer can be had (the owner cannot
// prompt, or the prompt failed), the error wraps core.ErrGitIdentityNotShared
// and the identity stays private. Only a canceled request fails otherwise.
func (srv *Server) AuthorizeGitIdentity(ctx context.Context) (core.GitIdentity, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return core.GitIdentity{}, err
	}
	owner, delegated, err := gitPushOwner(client)
	if err != nil {
		return core.GitIdentity{}, err
	}
	if delegated {
		own, err := servesAttachablesBelowModule(client)
		if err != nil {
			return core.GitIdentity{}, err
		}
		if own {
			// Without its attachables (yet), it has no config of its own to
			// read: ask the owner instead, rather than wait.
			identity, available, err := srv.clientGitIdentity(ctx, client, true)
			if err != nil || available {
				return identity, err
			}
		}
	}
	identity, _, err := srv.clientGitIdentity(ctx, owner, false)
	if err != nil {
		return core.GitIdentity{}, err
	}
	if identity == (core.GitIdentity{}) {
		return identity, nil
	}
	if !delegated && !core.IsAgentToolCall(ctx) {
		if _, ok := core.CallerAgent(ctx); !ok {
			return identity, nil
		}
	}
	spec := gitIdentityApproval(identity)
	memo := gitIdentityMemoKey(ctx, delegated, spec.Subject)
	if memo != "" {
		if decided, ok := client.gitIdentityDecisions.Load(memo); ok {
			decided := decided.(gitIdentityDecision)
			return decided.identity, decided.err
		}
	}
	allowed, err := srv.requestApproval(ctx, client, owner, spec)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return core.GitIdentity{}, ctxErr
		}
		// Nobody decided, so nothing is shared, as for a denial. Frontends
		// that cannot prompt do not all say so the same way (a report-mode
		// CLI fails the prompt outright), and a commit must not fail on it.
		err = fmt.Errorf("%w: %w", core.ErrGitIdentityNotShared, err)
		identity = core.GitIdentity{}
	} else if !allowed {
		err = fmt.Errorf("%w: denied", core.ErrGitIdentityNotShared)
		identity = core.GitIdentity{}
	}
	if memo != "" {
		client.gitIdentityDecisions.Store(memo, gitIdentityDecision{identity: identity, err: err})
	}
	return identity, err
}

type gitIdentityDecision struct {
	identity core.GitIdentity
	err      error
}

// gitIdentityMemoKey keys the answer for the call asking for subject, so
// evaluating that same call again (a module's SDK sends one query per field
// it reads, each re-running Workspace.withCommit, which is never cached)
// shares the one answer rather than asking again, whatever it was: even a
// denial or an allow-once holds for that call. Only delegated clients
// remember: they live for one module function call, or an exec within one,
// so nothing carries over to a later call. Empty when the call is unknown.
func gitIdentityMemoKey(ctx context.Context, delegated bool, subject string) string {
	if !delegated {
		return ""
	}
	call := dagql.CurrentCall(ctx)
	if call == nil {
		return ""
	}
	recipe, err := call.RecipeDigest(ctx)
	if err != nil {
		return ""
	}
	return recipe.String() + "\x00" + subject
}

// servesAttachablesBelowModule reports whether client, a non-module client,
// serves its own attachables (or those of a client of its exec) from below a
// module boundary, rather than inheriting or proxying the owner's: its Git
// config is then the module's container's, not the owner's.
func servesAttachablesBelowModule(client *clientRuntime) (bool, error) {
	if client.mod.Self() != nil {
		return false, nil
	}
	sess := client.daggerSession
	sess.scopeMu.Lock()
	inert := client.inertAttachables
	proxy := client.hostServiceProxyClientID
	attachables := client.attachablesClientID
	sess.scopeMu.Unlock()
	if inert || proxy != "" {
		return false, nil
	}
	if attachables == "" || attachables == client.clientID {
		return true, nil
	}
	parents, err := sess.ancestorRuntimes(client.clientRecord)
	if err != nil {
		return false, err
	}
	belowModule := false
	for _, parent := range parents {
		if parent.mod.Self() != nil {
			belowModule = true
			continue
		}
		if belowModule && parent.clientID == attachables {
			return true, nil
		}
	}
	return false, nil
}

// gitIdentityApproval asks the owner to share identity. The question shows the
// identity itself, quoted and escaped for terminals (including bidi/control
// characters), not Markdown: Git config is not the code asking, but it is
// still not prose. The subject is that same identity, escaped, so a grant
// covers only the identity the owner saw: after a change to their Git config,
// they are asked again. (A name or email holding angle brackets could spell
// another identity's subject, but commits reject those anyway.)
func gitIdentityApproval(identity core.GitIdentity) approvalSpec {
	var shown string
	switch {
	case identity.Name != "" && identity.Email != "":
		shown = identity.Name + " <" + identity.Email + ">"
	case identity.Name != "":
		shown = identity.Name
	default:
		shown = "<" + identity.Email + ">"
	}
	return approvalSpec{
		Kind:     approvalKindGitIdentity,
		Subject:  literalText(shown),
		Question: gitIdentityPromptPrefix + strconv.QuoteToASCII(shown),
	}
}

// clientGitIdentity reads c's effective user.name and user.email at its
// working directory (outside a checkout, Git uses system/global config). A
// client without Git config, or one that does not serve it (a host policy can
// leave the Git service out), has no identity to share. With ifAvailable, a
// client whose attachables are not available reports so instead of waiting
// for them.
func (srv *Server) clientGitIdentity(ctx context.Context, c *clientRuntime, ifAvailable bool) (identity core.GitIdentity, available bool, _ error) {
	conn, available, err := srv.SpecificClientAttachableConn(ctx, c.clientID, core.SpecificClientAttachableConnOpts{IfAvailable: ifAvailable})
	if err != nil {
		if status.Code(err) == codes.PermissionDenied {
			return identity, true, nil
		}
		return identity, false, fmt.Errorf("read Git author config: %w", err)
	}
	if !available {
		return identity, false, nil
	}
	response, err := git.NewGitClient(conn).GetConfig(ctx, &git.GitConfigRequest{CheckoutPath: "."})
	switch status.Code(err) {
	case codes.OK:
	case codes.Unimplemented, codes.PermissionDenied:
		return identity, true, nil
	default:
		return identity, true, fmt.Errorf("read Git author config: %w", err)
	}
	if gitErr := response.GetError(); gitErr != nil {
		if gitErr.Type == git.NOT_FOUND || gitErr.Type == git.CONFIG_RETRIEVAL_FAILED {
			return identity, true, nil
		}
		return identity, true, fmt.Errorf("read Git author config: %s", gitErr.Message)
	}
	// git config -l lists less specific scopes first; the last value wins,
	// matching `git config --get`.
	for _, entry := range response.GetConfig().GetEntries() {
		switch strings.ToLower(entry.GetKey()) {
		case "user.name":
			identity.Name = entry.GetValue()
		case "user.email":
			identity.Email = entry.GetValue()
		}
	}
	return identity, true, nil
}
