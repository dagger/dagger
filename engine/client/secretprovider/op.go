package secretprovider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func opProvider(ctx context.Context, key string) ([]byte, error) {
	key = "op://" + key

	if _, err := exec.LookPath("op"); err != nil {
		return nil, fmt.Errorf("unable to lookup %s: op is not installed", key)
	}

	cmd := exec.CommandContext(
		ctx,
		"op",
		"read",
		"-n",
		key,
	)
	cmd.Env = os.Environ()

	plaintext, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("unable to lookup %s: %w", key, err)
	}

	return plaintext, nil
}
