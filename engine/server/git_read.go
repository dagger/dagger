package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
)

const approvalKindGitRead = "git-read"

// gitReadPromptPrefix starts every read approval prompt. The TUI renders it as
// an ordinary question, and tests wait for it.
const gitReadPromptPrefix = "Allow an agent to read "

// AuthorizeGitRead returns the client whose Git credentials may authenticate
// a read of remote, an address an agent's model supplied as a tool argument.
//
// The owner is the trusted caller before the first module boundary, as for
// push. When the caller is that owner itself (the user's own conversation),
// its credentials apply as they would to any of its reads. When a module
// drives the agent (e.g. a worker spawned by a module), the module chose the
// conversation, so the owner approves each remote, in its canonical spelling
// (see canonicalGitReadRemote), for that agent and module first (see
// requestApproval): another module, even one only named like it, asks again,
// as the prompt naming the module implies. A denial holds only for that
// attempt. Otherwise any module could read whatever the user can, by
// prompting a model.
//
// What "allow once" covers: Query.git asks here once per credentialed lookup,
// and that lookup is cached per calling client and URL as spelled
// (gitPerClientInput, in its agent-address namespace). So a once grant lasts,
// in effect, as long as that client's cached lookup of that spelling: the
// same client resolving the same URL again reuses the repository without
// asking, while another spelling or another client asks again. Either way,
// the repository a lookup returns carries the owner's token as an explicit
// argument, so whatever the agent derives from it (refs, trees) reads with it
// for as long as it lives. Making the lookup per-call would not change that,
// only ask more often and run the credential helper on every lookup, so
// "once" means one repository handle, not one network read.
func (srv *Server) AuthorizeGitRead(ctx context.Context, remote string) (*engine.ClientMetadata, error) {
	// The remote is model-supplied: reject before prompting, and never echo
	// what may hold credentials.
	parsed, err := canonicalGitReadRemote(remote)
	if err != nil {
		return nil, err
	}
	remote = parsed.Remote()
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	owner, delegated, err := gitPushOwner(client)
	if err != nil {
		return nil, err
	}
	if !delegated {
		return owner.daggerSession.clientMetadataSnapshot(owner.clientRecord)
	}
	allowed, err := srv.requestApproval(ctx, client, owner, gitReadApproval(remote))
	if err != nil {
		return nil, fmt.Errorf("git read with the owner's credentials requires approval: %w", err)
	}
	if !allowed {
		return nil, fmt.Errorf("git read of %s with the owner's credentials was denied by the owning client", literalText(remote))
	}
	return owner.daggerSession.clientMetadataSnapshot(owner.clientRecord)
}

// gitReadApproval asks the owner to approve a read of remote, a canonical
// remote (see canonicalGitReadRemote). The URL is model-supplied, so it is
// literal text, escaped for terminals (including bidi/control characters),
// not Markdown.
func gitReadApproval(remote string) approvalSpec {
	return approvalSpec{
		Kind:     approvalKindGitRead,
		Subject:  literalText(remote),
		Question: gitReadPromptPrefix + literalText(remote) + " with your Git credentials",
	}
}

func literalText(s string) string {
	quoted := strconv.QuoteToASCII(s)
	return quoted[1 : len(quoted)-1]
}

var errInvalidGitReadRemote = errors.New("invalid git remote URL")

// canonicalGitReadRemote validates a remote an agent may read with its owner's
// credentials, as AuthorizeGitPush validates a destination, and spells it the
// one way approvals are keyed by: the host lowercased, and a trailing "/" or
// ".git" dropped.
func canonicalGitReadRemote(remote string) (*gitutil.GitURL, error) {
	if strings.ContainsAny(remote, "\r\n\x00?#") {
		return nil, errInvalidGitReadRemote
	}
	parsed, err := gitutil.ParseURL(remote)
	if err != nil || parsed.Host == "" || parsed.Fragment != nil {
		return nil, errInvalidGitReadRemote
	}
	if parsed.User != nil {
		return nil, errors.New("git remote URL must not contain embedded credentials")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), ".git")
	if strings.Trim(parsed.Path, "/") == "" {
		return nil, errInvalidGitReadRemote
	}
	return parsed, nil
}
