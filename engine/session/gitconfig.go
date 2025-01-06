package session

import (
	"bufio"
	bytes "bytes"
	context "context"
	fmt "fmt"
	"net/url"
	"os"
	"os/exec"
	strings "strings"
	"sync"
	"time"

	grpc "google.golang.org/grpc"
)

var gitConfigMutex sync.Mutex

type GitConfigAttachable struct {
	rootCtx context.Context

	UnimplementedGitConfigSvcServer
}

func NewGitConfigAttachable(rootCtx context.Context) GitConfigAttachable {
	return GitConfigAttachable{
		rootCtx: rootCtx,
	}
}

func (s GitConfigAttachable) Register(srv *grpc.Server) {
	RegisterGitConfigSvcServer(srv, &s)
}

func newGitConfigErrorResponse(errorType GitConfigErrorInfo_GitConfigErrorType, message string) *GetGitConfigResponse {
	return &GetGitConfigResponse{
		Result: &GetGitConfigResponse_Error{
			Error: &GitConfigErrorInfo{
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
func (s GitConfigAttachable) GetGitConfig(ctx context.Context, req *GetGitConfigRequest) (*GetGitConfigResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Check if git is installed
	if _, err := exec.LookPath("git"); err != nil {
		return newGitConfigErrorResponse(GC_NO_GIT, "Git is not installed or not in PATH"), nil
	}

	// Ensure no parallel execution of the git CLI happens
	gitConfigMutex.Lock()
	defer gitConfigMutex.Unlock()

	// Prepare the git credential fill command
	cmd := exec.CommandContext(ctx, "git", "config", "-l")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"SSH_ASKPASS=echo",
	)

	// Run the command
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return newGitConfigErrorResponse(GC_TIMEOUT, "Git config command timed out"), nil
		}
		return newGitConfigErrorResponse(GC_GIT_CONFIG_RETRIEVAL_FAILED, fmt.Sprintf("Failed to retrieve config: %v", err)), nil
	}

	// Parse the output
	config, err := parseGitConfigOutput(stdout.Bytes())
	if err != nil {
		return newGitConfigErrorResponse(GC_GIT_CONFIG_RETRIEVAL_FAILED, fmt.Sprintf("Failed to retrieve credentials: %v", err)), nil
	}

	return &GetGitConfigResponse{
		Result: &GetGitConfigResponse_Config{
			config,
		},
	}, nil
}

func parseGitConfigOutput(output []byte) (*GitConfig, error) {
	if len(output) == 0 {
		return nil, fmt.Errorf("no output from credential helper")
	}

	goprivatesMap := make(map[string]struct{})
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

		if strings.HasPrefix(parts[0], "url.") && strings.HasSuffix(parts[0], ".insteadof") {
			parsed, err := url.Parse(parts[1])
			if err != nil {
				return nil, err
			}

			goprivatesMap[parsed.Host] = struct{}{}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading credential helper output: %w", err)
	}

	goprivates := make([]string, len(goprivatesMap))
	index := 0
	for k := range goprivatesMap {
		goprivates[index] = k
		index++
	}

	return &GitConfig{
		Content:   string(output),
		Goprivate: strings.Join(goprivates, ","),
	}, nil
}
