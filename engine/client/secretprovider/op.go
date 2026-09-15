package secretprovider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/1password/onepassword-sdk-go"
	"github.com/dagger/dagger/engine"
)

type opCacheEntry struct {
	expiresAt time.Time
	data      []byte
}

var (
	opCacheMu sync.Mutex
	opCache   = make(map[string]opCacheEntry)
)

func opProvider(ctx context.Context, key string) ([]byte, error) {
	cacheKey, ttl, err := parseOpCacheKey(key)
	if err != nil {
		return nil, err
	}

	opCacheMu.Lock()
	defer opCacheMu.Unlock()

	if existing, ok := opCache[cacheKey]; ok && !opCacheExpired(existing) {
		return append([]byte(nil), existing.data...), nil
	}

	plaintext, err := resolveOp(ctx, cacheKey)
	if err != nil {
		return nil, err
	}
	entry := opCacheEntry{data: append([]byte(nil), plaintext...)}
	if ttl > 0 {
		entry.expiresAt = time.Now().Add(ttl)
	}
	opCache[cacheKey] = entry
	return append([]byte(nil), plaintext...), nil
}

func parseOpCacheKey(key string) (string, time.Duration, error) {
	ref, rawQuery, hasQuery := strings.Cut(key, "?")
	cacheKey := "op://" + ref
	if !hasQuery {
		return cacheKey, 0, nil
	}

	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", 0, fmt.Errorf("invalid query parameters in secret key %q: %w", key, err)
	}

	ttlStr := strings.TrimSpace(query.Get("ttl"))
	if ttlStr == "" {
		return "op://" + key, 0, nil
	}

	ttl, err := time.ParseDuration(ttlStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid ttl %q provided for secret %q: %w", ttlStr, key, err)
	}

	return cacheKey, ttl, nil
}

func opCacheExpired(entry opCacheEntry) bool {
	return !entry.expiresAt.IsZero() && !entry.expiresAt.After(time.Now())
}

func resolveOp(ctx context.Context, key string) ([]byte, error) {
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
