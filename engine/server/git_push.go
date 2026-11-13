package server

import (
	"context"
	"fmt"
	"net/url"
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
	// The TUI highlights a leading "Allow force-push" in red, so keep the
	// wording in sync with dagql/idtui's forcePushPromptPrefix. Literal text,
	// escaped for terminals (including bidi/control characters), not Markdown.
	// Neither credentials nor module-supplied prose belong here.
	subject := literalText(remote) + " @ " + literalText(ref)
	spec := approvalSpec{
		Kind:     approvalKindGitPush,
		Subject:  subject,
		Question: "Allow push to " + subject,
	}
	if force {
		spec.Kind = approvalKindGitForcePush
		spec.Question = "Allow force-push to " + subject
		spec.Danger = true
	}
	return spec
}

// AuthorizeGitPush resolves the destination of a push of ref to remote and
// returns the owner whose credentials it may use. A direct call by the owner
// is its own authorization; a module's or an agent's push asks the owner
// first, about the exact ref for the remote's default branch, a tag or any
// other non-branch ref, and otherwise about the remote, once (see
// git_push_policy.go).
//
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
	// The default branch and non-branch refs ask about the exact ref; other
	// branches only need the remote approved (see git_push_policy.go).
	var remoteDefault gitRemoteDefault
	if !gitPushRefAlwaysAsks(ref) {
		remoteDefault = srv.gitRemoteDefault(ctx, owner, remote)
	}
	spec := gitPushApproval(remote, ref, force)
	if !gitPushNeedsRefApproval(ref, force, remoteDefault) {
		if srv.gitPushToOwnerOrigin(ctx, owner, remote) {
			return authorized()
		}
		spec = gitPushRemoteApproval(remote)
	}
	allowed, err := srv.requestApproval(ctx, client, owner, spec)
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
