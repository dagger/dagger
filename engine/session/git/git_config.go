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
	"time"
)

var gitConfigAllowedKeys = []string{}

func isGitConfigKeyAllowed(key string) bool {
	if slices.Contains(gitConfigAllowedKeys, key) {
		return true
	}

	if matchesURLInsteadOf(key) {
		return true
	}

	return false
}

func matchesURLInsteadOf(input string) bool {
	return strings.HasPrefix(input, "url.") && strings.HasSuffix(input, ".insteadof")
}

// GetConfig retrieves Git config using the local Git config system.
// The function has a timeout of 30 seconds and ensures thread-safe execution.
//
// It follows Git's config protocol and error handling:
// - If Git fails to list config: CONFIG_RETRIEVAL_FAILED
// - If the command times out: TIMEOUT
// - If Git is not installed: NOT_FOUND
// - If the request is invalid: INVALID_REQUEST
func (s GitAttachable) GetConfig(ctx context.Context, req *GitConfigRequest) (*GitConfigResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Check if git is installed
	if _, err := exec.LookPath("git"); err != nil {
		return newGitConfigErrorResponse(NOT_FOUND, "git is not installed or not in PATH"), nil
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
