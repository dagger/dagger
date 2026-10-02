package secretprovider

import (
	"context"
	"errors"
	"sync"
)

// LLMConfigResolver serves the llmconfig:// scheme: credentials the dagger
// CLI holds for the engine's LLM router (an API key stored in the user's
// config file, a subscription OAuth token it keeps refreshed). The path is
// "<provider>/<field>", e.g. "anthropic/auth_token"; see
// hack/designs/llm-config-transport.md for the fields.
//
// Only the CLI can serve it — it owns the config file and the OAuth refresh
// flows — so it registers the resolver at startup via
// RegisterLLMConfigResolver. The scheme itself is always known, so that
// `secret(uri:"llmconfig://...")` validates in the engine; resolving one
// where no resolver is registered is an error.
//
// A resolver may be handed a RejectedSecretValue fingerprint in the context:
// the consumer has found the value it last served unusable, and the resolver
// should rotate it if it can rather than serve it again.
type LLMConfigResolver func(ctx context.Context, path string) ([]byte, error)

// ErrNoLLMConfigResolver reports a llmconfig:// lookup in a process that
// serves no LLM configuration (anything but the dagger CLI).
var ErrNoLLMConfigResolver = errors.New("llmconfig:// secrets are only served by the dagger CLI")

var (
	llmConfigResolverMu sync.RWMutex
	llmConfigResolver   LLMConfigResolver
)

// RegisterLLMConfigResolver installs the resolver behind the llmconfig://
// scheme. Passing nil clears it. Safe to call concurrently.
func RegisterLLMConfigResolver(r LLMConfigResolver) {
	llmConfigResolverMu.Lock()
	defer llmConfigResolverMu.Unlock()
	llmConfigResolver = r
}

func llmConfigProvider(ctx context.Context, path string) ([]byte, error) {
	llmConfigResolverMu.RLock()
	r := llmConfigResolver
	llmConfigResolverMu.RUnlock()
	if r == nil {
		return nil, ErrNoLLMConfigResolver
	}
	return r(ctx, path)
}
