package engineutil

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/git"
)

// PrepareGitSSHAuth must only be called for an authorized push or a snapshot of
// the owning client's checkout, with that client's metadata. Ordinary Git reads
// must not initialize or unlock an agent. This neither registers nor returns a
// socket handle; the caller decides how to scope the prepared socket.
func (c *Client) PrepareGitSSHAuth(ctx context.Context, remote string) (string, error) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", err
	}
	caller, err := c.GetHostServiceCaller(ctx, md.ClientID)
	if err != nil {
		return "", err
	}
	// Keep the original RPC name for compatibility with existing CLI clients.
	if !caller.Supports("/dagger.git.Git/PreparePushSSHAuth") {
		return "", fmt.Errorf("SSH authentication needs an SSH agent or a CLI supporting automatic SSH authentication; upgrade the dagger CLI")
	}
	response, err := git.NewGitClient(caller.Conn()).PreparePushSSHAuth(ctx, &git.PreparePushSSHAuthRequest{Remote: remote})
	if err != nil {
		return "", err
	}
	if response.SocketPath == "" {
		return "", fmt.Errorf("client did not provide SSH authentication")
	}
	return response.SocketPath, nil
}
