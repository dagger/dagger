package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/dagger/dagger/util/gitutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// gitReadApprovalKey is what an owner approves an agent to read: one
// repository, in its canonical spelling (see canonicalGitReadRemote), with
// that owner's credentials.
type gitReadApprovalKey struct {
	owner, remote string
}

type gitReadApprovals = gitApprovals[gitReadApprovalKey]

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
// conversation, so the owner approves each remote first; a grant lasts for the
// session, a denial only for that attempt. Otherwise any module could read
// whatever the user can, by prompting a model.
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
	requester, err := gitReadRequester(client)
	if err != nil {
		return nil, err
	}
	key := gitReadApprovalKey{owner: owner.clientID, remote: remote}
	allowed, err := client.daggerSession.gitReadApprovals.check(ctx, key, func(ctx context.Context) (bool, error) {
		conn, available, err := srv.SpecificClientAttachableConn(ctx, owner.clientID, core.SpecificClientAttachableConnOpts{IfAvailable: true})
		if err != nil {
			return false, err
		}
		if !available {
			return false, fmt.Errorf("owning client is not available to approve the read")
		}
		response, err := prompt.NewPromptClient(conn).PromptBool(ctx, &prompt.BoolRequest{
			Prompt: gitReadPrompt(remote, requester),
		})
		if status.Code(err) == codes.Unimplemented {
			return false, fmt.Errorf("owning client cannot prompt (not interactive)")
		}
		if err != nil {
			return false, err
		}
		return response.Response, nil
	})
	if err != nil {
		return nil, fmt.Errorf("git read with the owner's credentials requires approval: %w", err)
	}
	if !allowed {
		return nil, fmt.Errorf("git read of %s with the owner's credentials was denied by the owning client", literalText(remote))
	}
	return owner.daggerSession.clientMetadataSnapshot(owner.clientRecord)
}

// gitReadPrompt asks the owner to approve a read of remote. Every value in it
// is literal text, escaped for terminals (including bidi/control characters),
// not Markdown: the URL is model-supplied, the module name module-supplied.
func gitReadPrompt(remote, requester string) string {
	var by string
	if requester != "" {
		by = " (requested by module " + strconv.QuoteToASCII(requester) + ")"
	}
	return fmt.Sprintf("%s%s with your Git credentials%s?", gitReadPromptPrefix, literalText(remote), by)
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

// gitReadRequester names the module nearest the caller: the one whose code
// drives the agent asking to read.
func gitReadRequester(client *clientRuntime) (string, error) {
	if mod := client.mod.Self(); mod != nil {
		return mod.Name(), nil
	}
	parents, err := client.daggerSession.ancestorRuntimes(client.clientRecord)
	if err != nil {
		return "", err
	}
	for i := len(parents) - 1; i >= 0; i-- {
		if mod := parents[i].mod.Self(); mod != nil {
			return mod.Name(), nil
		}
	}
	return "", nil
}
