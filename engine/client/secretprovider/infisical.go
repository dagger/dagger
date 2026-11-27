package secretprovider

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/dagger/dagger/internal/buildkit/session/secrets"
)

// infisicalProvider resolves infisical://PROJECT/ENV/[PATH/]SECRET using the
// Infisical CLI and its existing authentication and server configuration.
func infisicalProvider(ctx context.Context, key string) ([]byte, error) {
	parts := strings.SplitN(key, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" || strings.HasSuffix(parts[2], "/") {
		return nil, fmt.Errorf("invalid Infisical secret reference: expected PROJECT/ENV/[PATH/]SECRET")
	}

	folder, name := "/", parts[2]
	if i := strings.LastIndex(name, "/"); i >= 0 {
		folder, name = "/"+name[:i], name[i+1:]
	}

	cmd := exec.CommandContext(ctx, "infisical", "secrets", "get",
		"--projectId="+parts[0], "--env="+parts[1], "--path="+folder,
		"--plain", "--silent", "--", name,
	)
	plaintext, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("unable to lookup Infisical secret %q: %w", key, err)
	}
	// The CLI emits nothing for a missing secret, and a newline for an empty one.
	if len(plaintext) == 0 {
		return nil, fmt.Errorf("Infisical secret %q: %w", key, secrets.ErrNotFound)
	}
	// Remove only the newline added by the CLI, preserving the secret's whitespace.
	return bytes.TrimSuffix(plaintext, []byte("\n")), nil
}
