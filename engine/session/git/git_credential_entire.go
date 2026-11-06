package git

import (
	bytes "bytes"
	context "context"
	"errors"
	fmt "fmt"
	"os"
	"os/exec"
	strings "strings"
)

// Entire remotes (entire://) authenticate with a single bearer token, which
// git-remote-entire reads from ENTIRE_TOKEN. Entire does not install a git
// credential helper, so when none is configured the client looks for the
// token where Entire's own tools do: ENTIRE_TOKEN, then the Entire CLI's
// login (which honors ENTIRE_CONTEXT).
const (
	entireTokenEnv = "ENTIRE_TOKEN"
	entireCLI      = "entire"
)

func (s GitAttachable) getEntireCredentialFromEnv(_ context.Context, req *GitCredentialRequest) (*GitCredentialResponse, error) {
	token := strings.TrimSpace(os.Getenv(entireTokenEnv))
	if token == "" {
		return newGitCredentialErrorResponse(NOT_FOUND, entireTokenEnv+" is not set"), nil
	}
	return newEntireCredentialResponse(req, token), nil
}

// getEntireCredentialFromCLI runs `entire auth token`, which prints the
// selected login's token, refreshed if it has expired, and nothing else on
// stdout. It never prompts: without a login it fails with a hint on stderr.
func (s GitAttachable) getEntireCredentialFromCLI(ctx context.Context, req *GitCredentialRequest) (*GitCredentialResponse, error) {
	if _, err := exec.LookPath(entireCLI); err != nil {
		return newGitCredentialErrorResponse(NOT_FOUND, fmt.Sprintf("the Entire CLI is not available: %v", err)), nil
	}
	cmd := exec.CommandContext(ctx, entireCLI, "auth", "token")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return newGitCredentialErrorResponse(TIMEOUT, "entire auth token timed out"), nil
		}
		return newGitCredentialErrorResponse(CREDENTIAL_RETRIEVAL_FAILED,
			fmt.Sprintf("entire auth token failed: %s, %v", strings.TrimSpace(stderr.String()), err)), nil
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return newGitCredentialErrorResponse(CREDENTIAL_RETRIEVAL_FAILED, "entire auth token printed no token"), nil
	}
	return newEntireCredentialResponse(req, token), nil
}

// newEntireCredentialResponse returns token as the password. Entire has no
// username, and the engine sends none for entire:// remotes.
func newEntireCredentialResponse(req *GitCredentialRequest, token string) *GitCredentialResponse {
	return &GitCredentialResponse{
		Result: &GitCredentialResponse_Credential{
			Credential: &CredentialInfo{
				Protocol: req.Protocol,
				Host:     req.Host,
				Password: token,
			},
		},
	}
}
