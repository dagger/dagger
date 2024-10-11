package session

import (
	"bufio"
	bytes "bytes"
	context "context"
	"errors"
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

var errCredentialNotFound = errors.New("credential not found")

func (s GitCredentialAttachable) GetCredential(ctx context.Context, req *GitCredentialRequest) (*GitCredentialResponse, error) {

	fmt.Fprintf(os.Stderr, "🚀 Starting GetCredential function: |%+v|\n", req)

	// Create a new context with a timeout
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Validate request
	if req.Host == "" || req.Protocol == "" {
		fmt.Fprintf(os.Stderr, "❌ Invalid request: Host or Protocol is empty\n")
		return &GitCredentialResponse{
			Result: &GitCredentialResponse_Error{
				Error: &ErrorInfo{
					Type:    INVALID_REQUEST,
					Message: "Host and protocol are required",
				},
			},
		}, nil
	}

	fmt.Fprintf(os.Stderr, "✅ Request validated\n")

	// Check if git is installed
	if _, err := exec.LookPath("git"); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Git not found in PATH\n")
		return &GitCredentialResponse{
			Result: &GitCredentialResponse_Error{
				Error: &ErrorInfo{
					Type:    NO_GIT,
					Message: "Git is not installed or not in PATH",
				},
			},
		}, nil
	}

	fmt.Fprintf(os.Stderr, "✅ Git found in PATH\n")

	gitCredentialMutex.Lock()
	defer gitCredentialMutex.Unlock()

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

	// fmt.Fprintf(os.Stderr, "📝 Prepared input for git credential fill:\n%s", input)

	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", // Do not trigger the terminal password
		"SSH_ASKPASS=echo",      // Do not ask for SSH auth
	// "GIT_TRACE=1",
	)

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
		fmt.Fprintf(os.Stderr, "❌ Failed to retrieve credentials: %v\n", err)
		return &GitCredentialResponse{
			Result: &GitCredentialResponse_Error{
				Error: &ErrorInfo{
					Type:    CREDENTIAL_RETRIEVAL_FAILED,
					Message: fmt.Sprintf("Failed to retrieve credentials: %v", err),
				},
			},
		}, nil
	}

	fmt.Fprintf(os.Stderr, "✅ Git credential fill command completed successfully\n")

	// After running the command
	fmt.Fprintf(os.Stderr, "🔍 Git credential fill stdout:\n%s", stdout.String())
	fmt.Fprintf(os.Stderr, "🔍 Git credential fill stderr:\n%s", stderr.String())

	// Parse the output
	fmt.Fprintf(os.Stderr, "🔍 Parsing git credential output\n")
	cred, err := parseGitCredentialOutput(stdout.Bytes())
	if err != nil {
		if err == errCredentialNotFound {
			fmt.Fprintf(os.Stderr, "🚫 No matching credentials found\n")
			return &GitCredentialResponse{
				Result: &GitCredentialResponse_Error{
					Error: &ErrorInfo{
						Type:    CREDENTIAL_NOT_FOUND,
						Message: "No matching credentials found",
					},
				},
			}, nil
		}
		fmt.Fprintf(os.Stderr, "❌ Failed to parse git credential output: %v\n", err)
		return &GitCredentialResponse{
			Result: &GitCredentialResponse_Error{
				Error: &ErrorInfo{
					Type:    INVALID_CREDENTIAL_FORMAT,
					Message: fmt.Sprintf("Failed to parse git credential output: %v", err),
				},
			},
		}, nil
	}

	fmt.Fprintf(os.Stderr, "✅ Git credential output parsed successfully\n")

	// Return the credentials
	fmt.Fprintf(os.Stderr, "🎉 Returning credentials: |%+v|\n", cred)
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

	fmt.Fprintf(os.Stderr, "👾👾 output reader: |%s|\n", string(output))

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
