package git

import (
	"bufio"
	bytes "bytes"
	context "context"
	fmt "fmt"
	"os"
	"os/exec"
	"path/filepath"
	strings "strings"
	"time"

	"github.com/dagger/dagger/util/netrc"
)

// GetCredential retrieves Git credentials for the given request using the local Git credential system.
// The function has a timeout of 30 seconds and ensures thread-safe execution.
//
// It follows Git's credential helper protocol and error handling:
// - If Git can't find or execute a helper: CREDENTIAL_RETRIEVAL_FAILED
// - If a helper returns invalid format or no credentials: Git handles it as a failure (CREDENTIAL_RETRIEVAL_FAILED)
// - If the command times out: TIMEOUT
// - If Git is not installed: NOT_FOUND
// - If the request is invalid: INVALID_REQUEST
func (s GitAttachable) GetCredential(ctx context.Context, req *GitCredentialRequest) (*GitCredentialResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Validate request
	if req.Host == "" || req.Protocol == "" {
		return newGitCredentialErrorResponse(INVALID_REQUEST, "Host and protocol are required"), nil
	}

	methods := []func(context.Context, *GitCredentialRequest) (*GitCredentialResponse, error){
		s.getCredentialFromHelper,
		s.getCredentialFromNetrc,
	}

	var firstResp *GitCredentialResponse
	for _, method := range methods {
		resp, err := method(ctx, req)
		if err != nil {
			return nil, err
		}

		if firstResp == nil {
			firstResp = resp
		}
		if _, ok := resp.Result.(*GitCredentialResponse_Error); ok {
			continue
		}
		return resp, nil
	}
	return firstResp, nil
}

func (s GitAttachable) getCredentialFromHelper(ctx context.Context, req *GitCredentialRequest) (*GitCredentialResponse, error) {
	// Check if git is installed
	if _, err := exec.LookPath("git"); err != nil {
		return newGitCredentialErrorResponse(NOT_FOUND, "Git is not installed or not in PATH"), nil
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

	// Never prompt the user for credentials
	cmd.Env = append(os.Environ(),
		"GIT_ASKPASS=",          // Do not use external programs to ask for credentials
		"GIT_TERMINAL_PROMPT=0", // Disable Git's builtin prompting
	)

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

func (s GitAttachable) getCredentialFromNetrc(ctx context.Context, req *GitCredentialRequest) (*GitCredentialResponse, error) {
	if req.Protocol != "http" && req.Protocol != "https" {
		return newGitCredentialErrorResponse(INVALID_REQUEST, "netrc only supports http and https protocols"), nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return newGitCredentialErrorResponse(NOT_FOUND, "Failed to determine user home directory"), nil
	}
	file, err := os.Open(filepath.Join(homeDir, ".netrc"))
	if err != nil {
		if os.IsNotExist(err) {
			return newGitCredentialErrorResponse(NOT_FOUND, ".netrc file not found"), nil
		}
		return newGitCredentialErrorResponse(CREDENTIAL_RETRIEVAL_FAILED, "Failed to open .netrc file"), nil
	}
	defer file.Close()

	entries := netrc.NetrcEntries(file)
	for entry := range entries {
		if entry.Machine == "" || entry.Machine == req.Host {
			cred := &CredentialInfo{
				Protocol: req.Protocol,
				Host:     req.Host,
				Username: entry.Login,
				Password: entry.Password,
			}
			return &GitCredentialResponse{
				Result: &GitCredentialResponse_Credential{
					Credential: cred,
				},
			}, nil
		}
	}

	return newGitCredentialErrorResponse(CREDENTIAL_RETRIEVAL_FAILED, "No matching credentials found in .netrc"), nil
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
