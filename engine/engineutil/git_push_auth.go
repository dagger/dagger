package engineutil

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/git"
)

// PrepareGitPushSSHAuth must only be called for an authorized push, with the
// owning client's metadata. It neither registers nor returns a socket handle.
func (c *Client) PrepareGitPushSSHAuth(ctx context.Context, remote string) (string, error) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", err
	}
	caller, err := c.GetHostServiceCaller(ctx, md.ClientID)
	if err != nil {
		return "", err
	}
	if !caller.Supports("/dagger.git.Git/PreparePushSSHAuth") {
		return "", fmt.Errorf("SSH push needs an SSH agent or a CLI supporting automatic SSH authentication; upgrade the dagger CLI")
	}
	response, err := git.NewGitClient(caller.Conn()).PreparePushSSHAuth(ctx, &git.PreparePushSSHAuthRequest{Remote: remote})
	if err != nil {
		return "", err
	}
	if response.SocketPath == "" {
		return "", fmt.Errorf("client did not provide SSH push authentication")
	}
	return response.SocketPath, nil
}
