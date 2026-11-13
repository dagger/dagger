package server

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/util/gitutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	approvalKindGitPush      = "git-push"
	approvalKindGitForcePush = "git-force-push"
)

// gitPushApproval is the approval a delegated push needs: one exact ref of one
// resolved destination, force or not. The ref passed check-ref-format (no
// spaces), so the subject's " @ " separates the two unambiguously.
func gitPushApproval(remote, ref string, force bool) approvalSpec {
	// The TUI highlights a leading "Allow force-push" (or, in the bare
	// action, "Force-push") in red, so keep the wording in sync with
	// dagql/idtui's forcePushPromptPrefix and forcePushAction. The remote is
	// the resolved destination, not what the caller passed. Literal text,
	// escaped for terminals (including bidi/control characters), not Markdown.
	// Neither credentials nor module-supplied prose belong here.
	subject := literalText(remote) + " @ " + literalText(ref)
	spec := approvalSpec{
		Kind:     approvalKindGitPush,
		Subject:  subject,
		Question: "Allow push to " + subject,
		Action:   "Push to " + subject,
	}
	if force {
		spec.Kind = approvalKindGitForcePush
		spec.Question = "Allow force-push to " + subject
		spec.Action = "Force-push to " + subject
		spec.Danger = true
	}
	return spec
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
		if password || parsed.Scheme == gitutil.HTTPProtocol || parsed.Scheme == gitutil.HTTPSProtocol ||
			parsed.Scheme == gitutil.EntireProtocol {
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
	allowed, err := srv.requestApproval(ctx, client, owner, gitPushApproval(remote, ref, force))
	if err != nil {
		return nil, fmt.Errorf("git push requires approval: %w", err)
	}
	if !allowed {
		action := "push"
		if force {
			action = "force push"
		}
		return nil, fmt.Errorf("git %s permission denied by the owning client for %s", action, ref)
	}
	return authorized()
}

// gitPushOwner returns the trusted client whose host and authority client's
// request is made with, and whether client acts on its behalf (delegated), so
// the request needs the owner's approval.
//
// Walking the ancestry root first, the owner is the last client before the
// first boundary, where a boundary is a module client or an inert client (one
// without a host of its own, see clientRecord.inertAttachables, inherited by
// its nested clients). It is not the nearest client with a host: a module
// cannot regain implicit authorization by spawning a non-module nested client,
// so no client after the first boundary ever counts.
//
// An inert client, e.g. an in-engine SDK client evaluating an agent's snippet,
// is a boundary even when it serves no module: its host access ends there, so
// it cannot be its own owner (prompting it, or reading through it, could only
// be denied). Escalating to the client that started it is deliberate, and is
// always gated by the owner's approval.
func gitPushOwner(client *clientRuntime) (*clientRuntime, bool, error) {
	sess := client.daggerSession
	parents, err := sess.ancestorRuntimes(client.clientRecord)
	if err != nil {
		return nil, false, err
	}
	chain := append(slices.Clip(parents), client)
	boundary := make([]bool, len(chain))
	sess.scopeMu.Lock()
	for i, c := range chain {
		boundary[i] = c.inertAttachables
	}
	sess.scopeMu.Unlock()
	for i, c := range chain {
		if c.mod.Self() == nil && !boundary[i] {
			continue
		}
		if i == 0 {
			return nil, true, fmt.Errorf("no owning client for git push")
		}
		return chain[i-1], true, nil
	}
	return client, false, nil
}
