package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const gitMissingMessage = "git is not installed or not in PATH"

// runHostGit runs a git command against a local checkout, returning its
// standard output. Errors carry git's standard error, which is where it
// explains what it refused to do.
func runHostGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"SSH_ASKPASS=echo",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git %s timed out", strings.Join(args, " "))
		}
		detail := strings.TrimSpace(stderr.String() + stdout.String())
		if detail == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, detail)
	}
	return stdout.String(), nil
}
