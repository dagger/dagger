package secretprovider

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

func cmdProvider(ctx context.Context, cmd string) ([]byte, error) {
	var stdoutBytes []byte
	var err error
	if runtime.GOOS == "windows" {
		stdoutBytes, err = exec.CommandContext(ctx, "cmd.exe", "/C", cmd).Output()
	} else {
		// #nosec G204
		stdoutBytes, err = exec.CommandContext(ctx, "sh", "-c", cmd).Output()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to run secret command %q: %w", cmd, err)
	}
	return trimCommandOutput(stdoutBytes), nil
}

// Like shell command substitution, strip trailing newlines, including CRLF
// emitted by Windows commands. Preserve spaces and embedded line endings.
func trimCommandOutput(output []byte) []byte {
	for bytes.HasSuffix(output, []byte("\n")) {
		output = bytes.TrimSuffix(output, []byte("\n"))
		output = bytes.TrimSuffix(output, []byte("\r"))
	}
	return output
}
