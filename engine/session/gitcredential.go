package session

import (
	"bufio"
	bytes "bytes"
	context "context"
	fmt "fmt"
	"os"
	"os/exec"
	strings "strings"
	"sync"
	"time"

	grpc "google.golang.org/grpc"
)

var gitCredentialMutex sync.Mutex

type GitCredentialAttachable struct {
	rootCtx context.Context

	UnimplementedGitCredentialServer
}

func NewGitCredentialAttachable(rootCtx context.Context) GitCredentialAttachable {
	return GitCredentialAttachable{
		rootCtx: rootCtx,
	}
}

func (s GitCredentialAttachable) Register(srv *grpc.Server) {
	RegisterGitCredentialServer(srv, s)
}

func newErrorResponse(errorType ErrorInfo_ErrorType, message string) *GitCredentialResponse {
	return &GitCredentialResponse{
		Result: &GitCredentialResponse_Error{
			Error: &ErrorInfo{
				Type:    errorType,
				Message: message,
			},
		},
	}
}

// GetCredential retrieves Git credentials for the given request using the local Git credential system.
// The function has a timeout of 30 seconds and ensures thread-safe execution.
//
// It follows Git's credential helper protocol and error handling:
// - If Git can't find or execute a helper: CREDENTIAL_RETRIEVAL_FAILED
// - If a helper returns invalid format or no credentials: Git handles it as a failure (CREDENTIAL_RETRIEVAL_FAILED)
// - If the command times out: TIMEOUT
// - If Git is not installed: NO_GIT
// - If the request is invalid: INVALID_REQUEST
func (s GitCredentialAttachable) GetCredential(ctx context.Context, req *GitCredentialRequest) (*GitCredentialResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Validate request
	if req.Host == "" || req.Protocol == "" {
		return newErrorResponse(INVALID_REQUEST, "Host and protocol are required"), nil
	}

	// Check if git is installed
	if _, err := exec.LookPath("git"); err != nil {
		return newErrorResponse(NO_GIT, "Git is not installed or not in PATH"), nil
	}

	// Ensure no parallel execution of the git CLI happens
	gitCredentialMutex.Lock()
	defer gitCredentialMutex.Unlock()

	// Prepare the git credential fill command
	cmd := exec.CommandContext(ctx, "git", "credential", "fill")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	// Prepare input
	input := fmt.Sprintf("protocol=%s\nhost=%s\n", req.Protocol, req.Host)
	if req.Path != "" {
		input += fmt.Sprintf("path=%s\n", req.Path)
	}
	input += "\n"
	cmd.Stdin = strings.NewReader(input)

	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"SSH_ASKPASS=echo",
	)

	// Run the command
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return newErrorResponse(TIMEOUT, "Git credential command timed out"), nil
		}
		return newErrorResponse(CREDENTIAL_RETRIEVAL_FAILED, fmt.Sprintf("Failed to retrieve credentials: %v", err)), nil
	}

	// Parse the output
	cred, err := parseGitCredentialOutput(stdout.Bytes())
	if err != nil {
		return newErrorResponse(CREDENTIAL_RETRIEVAL_FAILED, fmt.Sprintf("Failed to retrieve credentials: %v", err)), nil
	}

	return &GitCredentialResponse{
		Result: &GitCredentialResponse_Credential{
			Credential: cred,
		},
	}, nil
}

func parseGitCredentialOutput(output []byte) (*CredentialInfo, error) {
	if len(output) == 0 {
		return nil, fmt.Errorf("no output from credential helper")
	}

	cred := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid format: line doesn't match key=value pattern")
		}

		cred[parts[0]] = parts[1]
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading credential helper output: %w", err)
	}

	if cred["username"] == "" || cred["password"] == "" {
		// should not be possible
		return nil, fmt.Errorf("incomplete credentials: missing username or password")
	}

	return &CredentialInfo{
		Protocol: cred["protocol"],
		Host:     cred["host"],
		Username: cred["username"],
		Password: cred["password"],
	}, nil
}
