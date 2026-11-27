package secretprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// dopplerProvider resolves doppler://PROJECT/CONFIG/SECRET using the Doppler
// CLI and its existing authentication (doppler login or DOPPLER_TOKEN).
func dopplerProvider(ctx context.Context, key string) ([]byte, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("invalid Doppler secret reference %q: expected PROJECT/CONFIG/SECRET", key)
	}

	cmd := exec.CommandContext(ctx, "doppler", "secrets", "get",
		"--project="+parts[0], "--config="+parts[1],
		"--plain", "--silent", "--no-check-version", "--", parts[2],
	)
	plaintext, err := cmd.Output()
	if err != nil {
		// The CLI exits non-zero for missing secrets and auth failures; its
		// stderr carries the reason, never the secret value.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			err = fmt.Errorf("%w: %s", err, bytes.TrimSpace(exitErr.Stderr))
		}
		return nil, fmt.Errorf("unable to lookup Doppler secret %q: %w", key, err)
	}
	// --plain prints the value with fmt.Println: strip exactly that newline so
	// multiline values (e.g. PEM keys) are returned byte-for-byte.
	return bytes.TrimSuffix(plaintext, []byte("\n")), nil
}
