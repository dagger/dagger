package daggercmd

import (
	"context"
	"os"
	"slices"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

// oauthProviders are the subscription OAuth providers the CLI keeps fresh, by
// config-file name (which is also their wire name).
var oauthProviders = []string{"anthropic", "openai-codex"}

// assembleLLMConfig builds the LLM configuration this client sends to the
// engine, from the config file, the environment, and ./.env in the working
// directory (--workdir has already been applied to the process by then). See
// hack/designs/llm-config-transport.md.
//
// Commands that never touch an LLM must not break over LLM configuration, so
// a source that cannot be read is warned about and left out. A value that
// cannot be parsed (llmconfig.ErrMalformed) fails the command instead:
// dropping it would silently route differently from what the user asked for.
func assembleLLMConfig() (*engine.LLMConfig, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "." // the same directory, by a relative name
	}
	cfg, warnings, err := llmconfig.Assemble(cwd)
	for _, w := range warnings {
		slog.Warn("ignoring unreadable LLM configuration", "error", w)
	}
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// backgroundOAuthRefresh is replaceable so the refresher's scheduling and
// terminal-error behavior can be tested without a provider endpoint.
var backgroundOAuthRefresh = func(ctx context.Context, provider string) error {
	_, err := llmconfig.RefreshOAuthProviderIfNeeded(ctx, provider)
	return err
}

// Timings for the background refresher. Vars, not consts, so tests can shrink
// them — the same reason llmconfig's endpoint URLs are vars.
var (
	// oauthRefreshLead is how far ahead of a token's true expiry the refresher
	// wakes up. It matches the margin the expiry check applies, so the wake-up
	// finds the token due rather than just short of it.
	oauthRefreshLead = 5 * time.Minute
	// oauthRefreshUnknownInterval is the poll interval for a provider whose
	// token endpoint never said when the token expires. Unknown must not mean
	// "hot loop", nor "never look again".
	oauthRefreshUnknownInterval = 10 * time.Minute
	// oauthRefreshMinDelay keeps a token that is already past its refresh point
	// (a failing endpoint, a lifetime shorter than the lead) from spinning the
	// loop.
	oauthRefreshMinDelay = 30 * time.Second
)

// startOAuthTokenRefresher runs a goroutine that refreshes each enabled
// subscription OAuth provider shortly before its access token expires,
// persisting the rotated token to the config file. A `dagger shell` or
// `dagger agent` session easily outlives an hour-long access token, and
// refreshing ahead of expiry keeps the round-trip off the critical path: the
// engine's next llmconfig:// lookup already finds a fresh token.
//
// It returns a stop function, and is a no-op — no goroutine at all — unless a
// subscription provider is actually configured and enabled. The on-demand
// refresh in the llmconfig:// resolver remains the safety net; it is what
// covers a laptop that slept through the timer.
func startOAuthTokenRefresher(ctx context.Context) func() {
	providers := enabledOAuthProviders()
	if len(providers) == 0 {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(nextOAuthRefreshDelay(providers)):
			}
			remaining := providers[:0]
			for _, provider := range providers {
				err := backgroundOAuthRefresh(ctx, provider)
				if err != nil && ctx.Err() == nil {
					slog.WarnContext(ctx, "failed to refresh LLM OAuth token",
						"provider", provider, "error", err)
					if llmconfig.IsTerminalOAuthRefreshError(err) {
						// invalid_grant and a missing refresh token cannot recover on
						// the next 30-second tick. Warn once and leave on-demand
						// refreshes to surface the reauthentication requirement.
						continue
					}
				}
				remaining = append(remaining, provider)
			}
			providers = remaining
			if len(providers) == 0 {
				return
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// enabledOAuthProviders lists the configured, enabled subscription OAuth
// providers. One config read, no network — cheap enough to run for every
// command, including the ones that never talk to an LLM.
func enabledOAuthProviders() []string {
	cfg, err := llmconfig.Load()
	if err != nil || cfg == nil {
		return nil
	}
	var names []string
	for _, name := range oauthProviders {
		if p, ok := cfg.LLM.Providers[name]; ok && p.Enabled && p.IsOAuth() {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// nextOAuthRefreshDelay returns how long to wait before the next refresh pass.
// It re-reads the persisted config on every cycle rather than arming once from
// a remembered expiry, so a token another dagger process refreshed — and the
// expiry it wrote — is respected here too.
func nextOAuthRefreshDelay(providers []string) time.Duration {
	cfg, err := llmconfig.Load()
	if err != nil || cfg == nil {
		return oauthRefreshUnknownInterval
	}
	var (
		delay time.Duration
		found bool
	)
	for _, name := range providers {
		p, ok := cfg.LLM.Providers[name]
		if !ok || !p.Enabled || !p.IsOAuth() {
			continue
		}
		next := oauthRefreshUnknownInterval
		if expiresAt := p.TokenExpiresAtTime(); !expiresAt.IsZero() {
			// May be negative for a token that is already overdue; the floor
			// below turns that into a prompt retry rather than a spin.
			next = time.Until(expiresAt.Add(-oauthRefreshLead))
		}
		if !found || next < delay {
			delay, found = next, true
		}
	}
	if !found {
		// Every provider was removed or disabled while we were sleeping. Keep
		// looking cheaply in case one comes back (`/setup` in another
		// terminal) rather than pinning the goroutine forever.
		return oauthRefreshUnknownInterval
	}
	return max(delay, oauthRefreshMinDelay)
}

func init() {
	// Serve the llmconfig:// credentials the LLMConfig we send refers to: API
	// keys stored in the config file or ./.env, and subscription OAuth tokens,
	// refreshed when due or when the engine reports the current one rejected.
	// The engine resolves them against this client, only for the provider it
	// actually routes, so commands that never touch an LLM pay nothing.
	secretprovider.RegisterLLMConfigResolver(llmconfig.ResolveSecret)
}
