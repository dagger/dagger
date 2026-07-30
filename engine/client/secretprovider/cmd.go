package secretprovider

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

func cmdProvider(ctx context.Context, cmd string) ([]byte, error) {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.CommandContext(ctx, "cmd.exe", "/C", cmd)
	} else {
		// #nosec G204
		c = exec.CommandContext(ctx, "sh", "-c", cmd)
	}
	// Run relative to the workspace root (the dagger.json directory) rather
	// than inheriting the CLI's own CWD, so relative paths in cmd:// values
	// (e.g. a script checked into the repo) resolve consistently regardless
	// of which subdirectory `dagger` was invoked from. Empty when no
	// dagger.json was found, which preserves today's fallback behavior
	// (inherit the process's own CWD).
	c.Dir = workspaceRootFromContext(ctx)

	stdoutBytes, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run secret command %q: %w", cmd, err)
	}
	return stdoutBytes, nil
}
