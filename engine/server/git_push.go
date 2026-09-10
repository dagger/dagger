package server

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/prompt"
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

func (srv *Server) AuthorizeGitPush(ctx context.Context, remote, ref string, force bool) (*engine.ClientMetadata, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	owner, delegated, err := gitPushOwner(client)
	if err != nil {
		return nil, err
	}
	// Calling push directly is implicit authorization for this exact operation.
	// Tool execution in the owner's context is not a direct user API call.
	if !delegated && !core.IsAgentToolCall(ctx) {
		return owner.daggerSession.clientMetadataSnapshot(owner.clientRecord)
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
	return owner.daggerSession.clientMetadataSnapshot(owner.clientRecord)
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
