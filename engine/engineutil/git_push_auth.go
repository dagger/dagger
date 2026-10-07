package engineutil

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/git"
)

// PrepareGitSSHAuth must only be called for an authorized push or a snapshot
// of the owning client's checkout, with that client's metadata. Ordinary Git
// reads must not initialize or unlock an agent. This neither registers nor
// returns a socket handle; the caller decides how to scope the prepared socket.
func (c *Client) PrepareGitSSHAuth(ctx context.Context, remote string) (string, error) {
	return c.prepareGitSSHAuth(ctx, &git.PreparePushSSHAuthRequest{Remote: remote})
}

// PrepareGitSSHReadAuth is PrepareGitSSHAuth for an agent's read of a remote
// its owner authorized (Server.AuthorizeGitRead), with that owner's metadata.
// It never prompts to unlock a key: it uses a running or configured agent or
// unencrypted identity files, and fails on a key that needs a passphrase.
// (A CLI predating that option would still prompt; the read was approved
// first either way.)
func (c *Client) PrepareGitSSHReadAuth(ctx context.Context, remote string) (string, error) {
	return c.prepareGitSSHAuth(ctx, &git.PreparePushSSHAuthRequest{Remote: remote, NoUnlock: true})
}

func (c *Client) prepareGitSSHAuth(ctx context.Context, req *git.PreparePushSSHAuthRequest) (string, error) {
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
	response, err := git.NewGitClient(caller.Conn()).PreparePushSSHAuth(ctx, req)
	if err != nil {
		return "", err
	}
	if response.SocketPath == "" {
		return "", fmt.Errorf("client did not provide SSH authentication")
	}
	return response.SocketPath, nil
}
