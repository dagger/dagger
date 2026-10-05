package server

import (
	"context"
	"fmt"
	"strconv"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/prompt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AuthorizeGitRead returns the client whose Git credentials may authenticate
// a read of remote, an address an agent's model supplied as a tool argument.
//
// The owner is the trusted caller before the first module boundary, as for
// push. When the caller is that owner itself (the user's own conversation),
// its credentials apply as they would to any of its reads. When a module
// drives the agent (e.g. a worker spawned by a module), the module chose the
// conversation, so the owner approves each remote once per session first:
// otherwise any module could read whatever the user can, by prompting a model.
func (srv *Server) AuthorizeGitRead(ctx context.Context, remote string) (*engine.ClientMetadata, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	owner, delegated, err := gitPushOwner(client)
	if err != nil {
		return nil, err
	}
	if delegated {
		key := gitPushApprovalKey{owner: owner.clientID, remote: remote}
		allowed, err := client.daggerSession.gitReadApprovals.check(ctx, key, func(ctx context.Context) (bool, error) {
			conn, available, err := srv.SpecificClientAttachableConn(ctx, owner.clientID, core.SpecificClientAttachableConnOpts{IfAvailable: true})
			if err != nil {
				return false, err
			}
			if !available {
				return false, fmt.Errorf("owning client is not available to approve the read")
			}
			// Literal text, escaped for terminals; the URL is model-supplied.
			quoted := strconv.QuoteToASCII(remote)
			response, err := prompt.NewPromptClient(conn).PromptBool(ctx, &prompt.BoolRequest{
				Prompt: fmt.Sprintf("Allow an agent to read %s with your Git credentials?", quoted[1:len(quoted)-1]),
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
			return nil, fmt.Errorf("git read of %s with the owner's credentials was denied by the owning client for this session", remote)
		}
	}
	return owner.daggerSession.clientMetadataSnapshot(owner.clientRecord)
}
