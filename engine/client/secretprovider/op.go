package secretprovider

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/1password/onepassword-sdk-go"
	"github.com/dagger/dagger/engine"
)

func opProvider(ctx context.Context, key string) ([]byte, error) {
	key = "op://" + key

	// Attempt to use the `OP_SERVICE_ACCOUNT_TOKEN`
	if os.Getenv("OP_SERVICE_ACCOUNT_TOKEN") != "" {
		return opSDKProvider(ctx, key)
	}

	// If not set, fall back to the `op` CLI, if present
	if _, err := exec.LookPath("op"); err == nil {
		return opCLIProvider(ctx, key)
	}

	return nil, fmt.Errorf("unable to lookup %q: Neither `OP_SERVICE_ACCOUNT_TOKEN` is set nor `op` binary is present", key)
}

func opSDKProvider(ctx context.Context, key string) ([]byte, error) {
	token := os.Getenv("OP_SERVICE_ACCOUNT_TOKEN")

	client, err := onepassword.NewClient(
		ctx,
		onepassword.WithServiceAccountToken(token),
		onepassword.WithIntegrationInfo("dagger", engine.BaseVersion(engine.Version)),
	)
	if err != nil {
		return nil, err
	}
	secret, err := client.Secrets().Resolve(ctx, key)
	if err != nil {
		return nil, err
	}
	return []byte(secret), nil
}

func opCLIProvider(ctx context.Context, key string) ([]byte, error) {
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
		return nil, fmt.Errorf("unable to lookup %q: %w", key, err)
	}

	return plaintext, nil
}
