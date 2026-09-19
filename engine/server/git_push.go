package server

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/dagger/dagger/util/gitutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type gitPushApprovalKey struct {
	owner, remote, ref string
	force              bool
}

type gitPushApproval struct {
	done    chan struct{}
	allowed bool
	err     error
}

// Lives only in daggerSession, never in a result, recipe, or client prompt key.
// Remember denials too, so a tool retry cannot badger the user into approving.
type gitPushApprovals struct {
	mu        sync.Mutex
	decisions map[gitPushApprovalKey]*gitPushApproval
}

func (a *gitPushApprovals) check(ctx context.Context, key gitPushApprovalKey, ask func(context.Context) (bool, error)) (bool, error) {
	a.mu.Lock()
	if decision, ok := a.decisions[key]; ok {
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-decision.done:
			return decision.allowed, decision.err
		}
	}
	decision := &gitPushApproval{done: make(chan struct{})}
	if a.decisions == nil {
		a.decisions = make(map[gitPushApprovalKey]*gitPushApproval)
	}
	a.decisions[key] = decision
	a.mu.Unlock()
	decision.allowed, decision.err = ask(ctx)
	a.mu.Lock()
	// A canceled or unavailable prompt is not a user decision.
	if decision.err != nil {
		delete(a.decisions, key)
	}
	close(decision.done)
	a.mu.Unlock()
	return decision.allowed, decision.err
}

//nolint:gocyclo // keep owner routing, destination validation, and approval in one auditable flow
func (srv *Server) AuthorizeGitPush(ctx context.Context, remote, ref string, force, resolveURL bool) (*core.GitPushAuthorization, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	owner, delegated, err := gitPushOwner(client)
	if err != nil {
		return nil, err
	}
	if resolveURL {
		// Only read routing configuration here. Neither credential helpers nor
		// SSH agents may be consulted until this exact resolved URL is approved.
		conn, available, err := srv.SpecificClientAttachableConn(ctx, owner.clientID, core.SpecificClientAttachableConnOpts{IfAvailable: true})
		if err != nil {
			return nil, err
		}
		if available {
			response, err := git.NewGitClient(conn).GetConfig(ctx, &git.GitConfigRequest{})
			if err != nil && status.Code(err) != codes.Unimplemented {
				return nil, fmt.Errorf("read push owner routing configuration failed")
			}
			if response != nil && response.GetError() != nil && response.GetError().Type != git.NOT_FOUND {
				// A broken config must not silently push to the unrewritten URL.
				return nil, fmt.Errorf("read push owner routing configuration failed")
			}
			if response != nil && response.GetConfig() != nil {
				remote = git.ResolvePushURL(remote, response.GetConfig().Entries)
			}
		}
	}
	// Config can itself contain credentials or malformed URLs. Reject without
	// echoing it in a prompt, error, or receipt, and never downgrade to a local
	// path or a Git remote helper supplied by host configuration.
	parsed, err := gitutil.ParseURL(remote)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid push destination URL")
	}
	if parsed.User != nil {
		_, password := parsed.User.Password()
		if password || parsed.Scheme == gitutil.HTTPProtocol || parsed.Scheme == gitutil.HTTPSProtocol {
			return nil, fmt.Errorf("push destination must not contain embedded credentials")
		}
	}
	if strings.ContainsAny(remote, "\r\n\x00") || strings.Contains(remote, "?") || strings.Contains(remote, "#") {
		return nil, fmt.Errorf("invalid push destination URL")
	}
	if parsed.Scheme == gitutil.SSHProtocol && parsed.User == nil {
		parsed.User = url.User("git")
	}
	remote = parsed.Remote()
	authorized := func() (*core.GitPushAuthorization, error) {
		md, err := owner.daggerSession.clientMetadataSnapshot(owner.clientRecord)
		if err != nil {
			return nil, err
		}
		return &core.GitPushAuthorization{Owner: md, Remote: remote}, nil
	}
	// Calling push directly is implicit authorization for this exact operation.
	// Tool execution in the owner's context is not a direct user API call.
	if !delegated && !core.IsAgentToolCall(ctx) {
		return authorized()
	}
	key := gitPushApprovalKey{owner: owner.clientID, remote: remote, ref: ref, force: force}
	allowed, err := client.daggerSession.gitPushApprovals.check(ctx, key, func(ctx context.Context) (bool, error) {
		conn, available, err := srv.SpecificClientAttachableConn(ctx, owner.clientID, core.SpecificClientAttachableConnOpts{IfAvailable: true})
		if err != nil {
			return false, err
		}
		if !available {
			return false, fmt.Errorf("owning client is not available to approve the push")
		}
		action := "pushing"
		if force {
			action = "force pushing"
		}
		// Literal text, escaped for terminals (including bidi/control characters),
		// not Markdown. Neither credentials nor module-supplied prose belong here.
		literal := func(s string) string {
			quoted := strconv.QuoteToASCII(s)
			return quoted[1 : len(quoted)-1]
		}
		response, err := prompt.NewPromptClient(conn).PromptBool(ctx, &prompt.BoolRequest{
			Prompt: fmt.Sprintf("Allow %s to %s @ %s?", action, literal(remote), literal(ref)),
		})
		if err != nil {
			return false, err
		}
		return response.Response, nil
	})
	if err != nil {
		return nil, fmt.Errorf("git push requires approval: %w", err)
	}
	if !allowed {
		action := "push"
		if force {
			action = "force push"
		}
		return nil, fmt.Errorf("git %s permission denied by the owning client for %s for this session", action, ref)
	}
	return authorized()
}

// A module cannot regain implicit authorization by spawning a non-module
// nested client. Use the trusted caller immediately before the first module
// boundary, not the nearest non-module client after that boundary.
func gitPushOwner(client *clientRuntime) (*clientRuntime, bool, error) {
	var owner *clientRuntime
	parents, err := client.daggerSession.ancestorRuntimes(client.clientRecord)
	if err != nil {
		return nil, false, err
	}
	for _, parent := range parents {
		if parent.mod.Self() != nil {
			if owner == nil {
				return nil, true, fmt.Errorf("no owning client for git push")
			}
			return owner, true, nil
		}
		owner = parent
	}
	if client.mod.Self() != nil {
		if owner == nil {
			return nil, true, fmt.Errorf("no owning client for git push")
		}
		return owner, true, nil
	}
	return client, false, nil
}
