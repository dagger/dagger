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

// Strip a final LF or CRLF only from single-line output. Preserve multiline
// secrets verbatim, since formats such as private keys may need the final newline.
func trimCommandOutput(output []byte) []byte {
	if !bytes.HasSuffix(output, []byte("\n")) {
		return output
	}
	line := bytes.TrimSuffix(output, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\r"))
	if bytes.ContainsAny(line, "\r\n") {
		return output
	}
	return line
}
