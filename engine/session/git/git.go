package git

import (
	"bufio"
	bytes "bytes"
	context "context"
	fmt "fmt"
	"os"
	"os/exec"
	"slices"
	strings "strings"
	"sync"
	"time"

	grpc "google.golang.org/grpc"
)

var gitMutex sync.Mutex

type GitAttachable struct {
	rootCtx context.Context

	UnimplementedGitServer
}

func NewGitAttachable(rootCtx context.Context) GitAttachable {
	return GitAttachable{
		rootCtx: rootCtx,
	}
}

func (s GitAttachable) Register(srv *grpc.Server) {
	RegisterGitServer(srv, &s)

	// dagger/dagger#9323 renamed the GitCredential attachable to Git
	// it's easy to provide a fallback
	// TODO: simplify when we break client<->server compat
	serviceDesc := _Git_serviceDesc
	serviceDesc.ServiceName = "GitCredential"
	srv.RegisterService(&serviceDesc, &s)
}

func newGitCredentialErrorResponse(errorType ErrorInfo_ErrorType, message string) *GitCredentialResponse {
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
func (s GitAttachable) GetCredential(ctx context.Context, req *GitCredentialRequest) (*GitCredentialResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Validate request
	if req.Host == "" || req.Protocol == "" {
		return newGitCredentialErrorResponse(INVALID_REQUEST, "Host and protocol are required"), nil
	}

	// Check if git is installed
	if _, err := exec.LookPath("git"); err != nil {
		return newGitCredentialErrorResponse(NO_GIT, "Git is not installed or not in PATH"), nil
	}

	// Ensure no parallel execution of the git CLI happens
	gitMutex.Lock()
	defer gitMutex.Unlock()

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
	)
	if req.Protocol != "http" && req.Protocol != "https" {
		cmd.Env = append(cmd.Env, "SSH_ASKPASS=echo")
	}

	// Run the command
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return newGitCredentialErrorResponse(TIMEOUT, "Git credential command timed out"), nil
		}
		return newGitCredentialErrorResponse(CREDENTIAL_RETRIEVAL_FAILED, fmt.Sprintf("Failed to retrieve credentials: %v", err)), nil
	}

	// Parse the output
	cred, err := parseGitCredentialOutput(stdout.Bytes())
	if err != nil {
		return newGitCredentialErrorResponse(CREDENTIAL_RETRIEVAL_FAILED, fmt.Sprintf("Failed to retrieve credentials: %v", err)), nil
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

var gitConfigAllowedKeys = []string{}

func matchesURLInsteadOf(input string) bool {
	return strings.HasPrefix(input, "url.") && strings.HasSuffix(input, ".insteadof")
}

func isGitConfigKeyAllowed(key string) bool {
	if slices.Contains(gitConfigAllowedKeys, key) {
		return true
	}

	if matchesURLInsteadOf(key) {
		return true
	}

	return false
}

// GetConfig retrieves Git config using the local Git config system.
// The function has a timeout of 30 seconds and ensures thread-safe execution.
//
// It follows Git's config protocol and error handling:
// - If Git fails to list config: CONFIG_RETRIEVAL_FAILED
// - If the command times out: TIMEOUT
// - If Git is not installed: NO_GIT
// - If the request is invalid: INVALID_REQUEST
func (s GitAttachable) GetConfig(ctx context.Context, req *GitConfigRequest) (*GitConfigResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Check if git is installed
	if _, err := exec.LookPath("git"); err != nil {
		return newGitConfigErrorResponse(NO_GIT, "git is not installed or not in PATH"), nil
	}

	// Ensure no parallel execution of the git CLI happens
	gitMutex.Lock()
	defer gitMutex.Unlock()

	cmd := exec.CommandContext(ctx, "git", "config", "-l", "-z")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"SSH_ASKPASS=echo",
	)

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return newGitConfigErrorResponse(TIMEOUT, "git config command timed out"), nil
		}
		return newGitConfigErrorResponse(CONFIG_RETRIEVAL_FAILED, fmt.Sprintf("Failed to retrieve git config: %v.", err)), nil
	}

	list, err := parseGitConfigOutput(stdout.Bytes())
	if err != nil {
		return newGitConfigErrorResponse(CONFIG_RETRIEVAL_FAILED, fmt.Sprintf("Failed to parse git config %v", err)), nil
	}

	return &GitConfigResponse{
		Result: &GitConfigResponse_Config{
			Config: list,
		},
	}, nil
}

// parseGitConfigOutput parses the output of the "git config -l -z" command.
func parseGitConfigOutput(output []byte) (*GitConfig, error) {
	entries := []*GitConfigEntry{}
	if len(output) == 0 {
		return &GitConfig{
			Entries: []*GitConfigEntry{},
		}, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Split(splitOnNull)

	for scanner.Scan() {
		line := scanner.Text()

		key, value, found := strings.Cut(line, "\n")
		if !found || len(value) == 0 {
			continue
		}
		if isGitConfigKeyAllowed(strings.ToLower(key)) {
			entries = append(entries, &GitConfigEntry{
				Key:   key,
				Value: value,
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading git config output: %w", err)
	}

	return &GitConfig{
		Entries: entries,
	}, nil
}

func newGitConfigErrorResponse(errorType ErrorInfo_ErrorType, message string) *GitConfigResponse {
	return &GitConfigResponse{
		Result: &GitConfigResponse_Error{
			Error: &ErrorInfo{
				Type:    errorType,
				Message: message,
			},
		},
	}
}

func splitOnNull(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}

	if atEOF {
		return len(data), data, nil
	}

	return 0, nil, nil
}
