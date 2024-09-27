package session

import (
	"bufio"
	bytes "bytes"
	context "context"
	"errors"
	fmt "fmt"
	"os/exec"
	strings "strings"
	"time"

	grpc "google.golang.org/grpc"
)

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

var errCredentialNotFound = errors.New("credential not found")

func (s GitCredentialAttachable) GetCredential(ctx context.Context, req *GitCredentialRequest) (*GitCredentialResponse, error) {
	// Create a new context with a timeout
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Validate request
	if req.Host == "" || req.Protocol == "" {
		return &GitCredentialResponse{
			Result: &GitCredentialResponse_Error{
				Error: &ErrorInfo{
					Type:    INVALID_REQUEST,
					Message: "Host and protocol are required",
				},
			},
		}, nil
	}

	// Check if git is installed
	if _, err := exec.LookPath("git"); err != nil {
		return &GitCredentialResponse{
			Result: &GitCredentialResponse_Error{
				Error: &ErrorInfo{
					Type:    NO_GIT,
					Message: "Git is not installed or not in PATH",
				},
			},
		}, nil
	}

	// Prepare the git credential fill command
	cmd := exec.CommandContext(ctx, "git", "credential", "fill")

	// Set up input, output, and error buffers
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Prepare input
	var input string
	if req.Path != "" {
		input = fmt.Sprintf("protocol=%s\nhost=%s\npath=%s\n\n", req.Protocol, req.Host, req.Path)
	} else {
		input = fmt.Sprintf("protocol=%s\nhost=%s\n\n", req.Protocol, req.Host)
	}
	cmd.Stdin = strings.NewReader(input)

	// Run the command
	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &GitCredentialResponse{
				Result: &GitCredentialResponse_Error{
					Error: &ErrorInfo{
						Type:    TIMEOUT,
						Message: "Git credential command timed out",
					},
				},
			}, nil
		}

		// Handle other errors
		return &GitCredentialResponse{
			Result: &GitCredentialResponse_Error{
				Error: &ErrorInfo{
					Type:    CREDENTIAL_RETRIEVAL_FAILED,
					Message: fmt.Sprintf("Failed to retrieve credentials: %v", err),
				},
			},
		}, nil
	}

	// Parse the output
	cred, err := parseGitCredentialOutput(stdout.Bytes())
	if err != nil {
		if err == errCredentialNotFound {
			return &GitCredentialResponse{
				Result: &GitCredentialResponse_Error{
					Error: &ErrorInfo{
						Type:    CREDENTIAL_NOT_FOUND,
						Message: "No matching credentials found",
					},
				},
			}, nil
		}
		return &GitCredentialResponse{
			Result: &GitCredentialResponse_Error{
				Error: &ErrorInfo{
					Type:    INVALID_CREDENTIAL_FORMAT,
					Message: fmt.Sprintf("Failed to parse git credential output: %v", err),
				},
			},
		}, nil
	}

	// Return the credentials
	return &GitCredentialResponse{
		Result: &GitCredentialResponse_Credential{
			Credential: cred,
		},
	}, nil
}

func parseGitCredentialOutput(output []byte) (*CredentialInfo, error) {
	cred := &CredentialInfo{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	foundCredential := false

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := parts[0], parts[1]
		switch key {
		case "protocol":
			cred.Protocol = value
		case "host":
			cred.Host = value
		case "username":
			cred.Username = value
			foundCredential = true
		case "password":
			cred.Password = value
			foundCredential = true
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading git credential output: %v", err)
	}

	if !foundCredential {
		return nil, errCredentialNotFound
	}

	if cred.Username == "" || cred.Password == "" {
		return nil, fmt.Errorf("incomplete credential information")
	}

	return cred, nil
}
