package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/dagger/dagger/util/gitutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// gitReadApprovalKey is what an owner approves: one module (see
// gitReadRequester) driving an agent to read one repository, in its canonical
// spelling (see canonicalGitReadRemote), with that owner's credentials.
// Another module asks again, as the prompt naming the module implies.
type gitReadApprovalKey struct {
	owner, requester, remote string
}

type gitReadApprovals = gitApprovals[gitReadApprovalKey]

// gitReadPromptPrefix starts every read approval prompt. The TUI renders it as
// an ordinary question, and tests wait for it.
const gitReadPromptPrefix = "Allow an agent to read "

// AuthorizeGitRead returns the client whose Git credentials may authenticate
// a read of remote, an address an agent's model supplied as a tool argument.
//
// The owner is the trusted caller before the first module boundary, as for
// push. The owner approves each remote first when:
//
//   - a module drives the agent (e.g. a worker spawned by a module): the module
//     chose the conversation, and otherwise any module could read whatever the
//     user can by prompting a model. The grant is that module's;
//   - the remote is read over SSH, even in the owner's own conversation: an SSH
//     agent offers the user's keys to whichever host it is pointed at, unlike a
//     credential helper, which answers per host, so a prompt-injected model
//     could otherwise make the engine authenticate as the user anywhere.
//
// The owner's own conversation reads HTTP(S) remotes silently, with its
// credentials as for any of its reads. A grant lasts for the session, a denial
// only for that attempt.
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
	viaSSH := parsed.Scheme == gitutil.SSHProtocol
	if !delegated && !viaSSH {
		return owner.daggerSession.clientMetadataSnapshot(owner.clientRecord)
	}
	// The owner's own conversation has no requesting module.
	var requester dagql.ObjectResult[*core.Module]
	var requesterName string
	if delegated {
		requester, err = gitReadRequester(client)
		if err != nil {
			return nil, err
		}
		requesterName = requester.Self().Name()
	}
	key := gitReadApprovalKey{
		owner:     owner.clientID,
		requester: resolvedModuleLoadIdentity(requester),
		remote:    remote,
	}
	allowed, err := client.daggerSession.gitReadApprovals.check(ctx, key, func(ctx context.Context) (bool, error) {
		conn, available, err := srv.SpecificClientAttachableConn(ctx, owner.clientID, core.SpecificClientAttachableConnOpts{IfAvailable: true})
		if err != nil {
			return false, err
		}
		if !available {
			return false, fmt.Errorf("owning client is not available to approve the read")
		}
		response, err := prompt.NewPromptClient(conn).PromptBool(ctx, &prompt.BoolRequest{
			Prompt: gitReadPrompt(remote, viaSSH, requesterName),
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
func gitReadPrompt(remote string, viaSSH bool, requester string) string {
	using := "your Git credentials"
	if viaSSH {
		using = "your SSH keys"
	}
	var by string
	if requester != "" {
		by = " (requested by module " + strconv.QuoteToASCII(requester) + ")"
	}
	return fmt.Sprintf("%s%s with %s%s?", gitReadPromptPrefix, literalText(remote), using, by)
}

func literalText(s string) string {
	quoted := strconv.QuoteToASCII(s)
	return quoted[1 : len(quoted)-1]
}

var errInvalidGitReadRemote = errors.New("invalid git remote URL")

// canonicalGitReadRemote validates a remote an agent may read with its owner's
// credentials, as AuthorizeGitPush validates a destination, and spells it the
// one way approvals are keyed by: the host lowercased, SSH's default user
// "git" made explicit, and a trailing "/" or ".git" dropped. HTTP(S) and SSH
// spellings of a repository stay distinct, since each authenticates with
// different credentials.
func canonicalGitReadRemote(remote string) (*gitutil.GitURL, error) {
	if strings.ContainsAny(remote, "\r\n\x00?#") {
		return nil, errInvalidGitReadRemote
	}
	parsed, err := gitutil.ParseURL(remote)
	if err != nil || parsed.Host == "" || parsed.Fragment != nil {
		return nil, errInvalidGitReadRemote
	}
	if parsed.User != nil {
		_, password := parsed.User.Password()
		if password || parsed.Scheme != gitutil.SSHProtocol {
			return nil, errors.New("git remote URL must not contain embedded credentials")
		}
	}
	if parsed.Scheme == gitutil.SSHProtocol && parsed.User == nil {
		parsed.User = url.User("git")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), ".git")
	if strings.Trim(parsed.Path, "/") == "" {
		return nil, errInvalidGitReadRemote
	}
	return parsed, nil
}

// gitReadRequester returns the module nearest the caller: the one whose code
// drives the agent asking to read. Approvals are scoped to it by its load
// identity (source, pin and name), so another module, even one with the same
// name, asks again.
func gitReadRequester(client *clientRuntime) (dagql.ObjectResult[*core.Module], error) {
	if client.mod.Self() != nil {
		return client.mod, nil
	}
	parents, err := client.daggerSession.ancestorRuntimes(client.clientRecord)
	if err != nil {
		return dagql.ObjectResult[*core.Module]{}, err
	}
	for i := len(parents) - 1; i >= 0; i-- {
		if parents[i].mod.Self() != nil {
			return parents[i].mod, nil
		}
	}
	return dagql.ObjectResult[*core.Module]{}, errors.New("no module requesting the git read")
}
