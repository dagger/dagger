package secretprovider

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/1password/onepassword-sdk-go"
	"github.com/dagger/dagger/engine"
)

type opResolver struct{}

func (opResolver) Resolve(ctx context.Context, key string) ([]byte, error) {
	return opProvider(ctx, key)
}

func (opResolver) ResolveMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	if os.Getenv("OP_SERVICE_ACCOUNT_TOKEN") != "" {
		return opSDKProviderMany(ctx, keys)
	}

	resolver := SecretResolverFunc(opProvider)
	return resolver.ResolveMany(ctx, keys)
}

func opProvider(ctx context.Context, key string) ([]byte, error) {
	key = "op://" + key

	// Attempt to use the `OP_SERVICE_ACCOUNT_TOKEN`
	if os.Getenv("OP_SERVICE_ACCOUNT_TOKEN") != "" {
		return opSDKProvider(ctx, key)
	}

	// If not set, fallback to the `op` CLI, if present
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

func opSDKProviderMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	token := os.Getenv("OP_SERVICE_ACCOUNT_TOKEN")

	client, err := onepassword.NewClient(
		ctx,
		onepassword.WithServiceAccountToken(token),
		onepassword.WithIntegrationInfo("dagger", engine.BaseVersion(engine.Version)),
	)
	if err != nil {
		return nil, err
	}

	refs := make([]string, 0, len(keys))
	for _, key := range keys {
		refs = append(refs, "op://"+key)
	}

	resolved, err := client.Secrets().ResolveAll(ctx, refs)
	if err != nil {
		return nil, err
	}

	values := make(map[string][]byte, len(keys))
	for i, ref := range refs {
		resp, ok := resolved.IndividualResponses[ref]
		if !ok {
			return nil, fmt.Errorf("unable to lookup %q: missing response", ref)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("unable to lookup %q: %+v", ref, resp.Error)
		}
		if resp.Content == nil {
			return nil, fmt.Errorf("unable to lookup %q: empty response", ref)
		}
		values[keys[i]] = []byte(resp.Content.Secret)
	}

	return values, nil
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
