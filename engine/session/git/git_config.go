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

// gitConfigAllowedKeys names the config the engine may read from the client:
// commit identity, URL rewrites, and the origin remote URL (recorded on
// reconstructed checkout repositories so remote-aware tooling keeps working).
var gitConfigAllowedKeys = []string{"user.name", "user.email", "remote.origin.url"}

func isGitConfigKeyAllowed(key string) bool {
	if slices.Contains(gitConfigAllowedKeys, key) {
		return true
	}

	if matchesURLInsteadOf(key) || (strings.HasPrefix(key, "url.") && strings.HasSuffix(key, ".pushinsteadof")) {
		return true
	}

	return false
}

// ResolvePushURL applies the owner's URL-only routing configuration. Like Git,
// pushInsteadOf matches the original URL, falling back to insteadOf only when
// no push rewrite matches. Each pass uses the longest prefix, with the first
// entry winning ties. Callers must validate the result before displaying it or
// acquiring credentials.
// Already-resolved captured remotes and explicit push URLs must not use this.
func ResolvePushURL(remote string, entries []*GitConfigEntry) string {
	for _, suffix := range []string{".pushinsteadof", ".insteadof"} {
		longest := -1
		rewritten := remote
		for _, entry := range entries {
			if entry == nil || len(entry.Key) <= len("url.")+len(suffix) {
				continue
			}
			key := strings.ToLower(entry.Key)
			if !strings.HasPrefix(key, "url.") || !strings.HasSuffix(key, suffix) {
				continue
			}
			if len(entry.Value) > longest && strings.HasPrefix(remote, entry.Value) {
				longest = len(entry.Value)
				// The subsection (replacement URL) is case-sensitive.
				rewritten = entry.Key[len("url."):len(entry.Key)-len(suffix)] + strings.TrimPrefix(remote, entry.Value)
			}
		}
		if longest >= 0 {
			return rewritten
		}
	}
	return remote
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

	// Serialize the short-lived host config and credential helpers, which may
	// share user-level helper state.
	gitHelperMutex.Lock()
	defer gitHelperMutex.Unlock()

	cmd := exec.CommandContext(ctx, "git", "config", "-l", "-z")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	// Run from a temp directory to avoid reading local repo config.
	// We only need system/global config (e.g. url.*.insteadOf), and running
	// inside a git repo can fail fatally if the .git pointer is invalid
	// (e.g. a mounted git worktree whose gitdir target doesn't exist).
	cmd.Dir = os.TempDir()
	if req.GetCheckoutPath() != "" {
		cmd.Dir = req.GetCheckoutPath()
	}

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
