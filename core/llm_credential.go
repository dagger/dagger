package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
)

// Per-request credential resolution for LLM providers.
//
// A subscription OAuth access token lives about an hour and is rotated behind
// the session by the CLI, but everything downstream of routing captures it:
// a provider SDK client bakes the credential into its request options at
// construction (option.WithAuthToken / option.WithAPIKey), and (*LLM).Endpoint
// memoizes the routed endpoint for the life of a conversation. That memo is
// deliberate — re-routing means reloading the whole router, ~21 variables at
// two client round-trips each — so the fix is not to re-route more often but
// to stop treating the credential as part of the route: the endpoint carries a
// credential *source*, and the endpoint's HTTP transport asks it for the
// current token before every provider request.
//
// The source caches, so the common case costs nothing extra, and exposes an
// invalidation entry point so a 401 can force the next attempt back to the
// client (see sendQueryWithRetry).

const (
	// credentialRefreshTTL bounds how long a credential of *unknown* expiry is
	// reused. Resolving one round-trips to the client's session (env:// ->
	// secret provider -> the CLI's OAuth refresher), which is cheap next to an
	// LLM call but not free, and a streaming turn can issue several requests
	// back-to-back. Short enough that a rotation is picked up within seconds,
	// long enough to collapse a burst onto one resolution.
	credentialRefreshTTL = 30 * time.Second

	// credentialExpiryMargin is subtracted from a known expiry, so a token is
	// replaced slightly before the provider stops accepting it. It covers the
	// clock skew between client and engine plus the time between authenticating
	// a request and the provider reading it.
	credentialExpiryMargin = time.Minute

	// credentialMaxTTL caps how long a credential is cached even when its
	// expiry is an hour off. Expiry is not the only reason a token changes: a
	// fresh `dagger llm setup`, or another process re-logging in, rotates it
	// early, and a live session should follow within minutes rather than at the
	// old token's expiry.
	credentialMaxTTL = 5 * time.Minute

	// credentialResolveTimeout bounds a single resolution. It runs detached
	// from the request that needs it (see credentialResolver.detach) and the
	// client's refresher hook may do a network round trip inside it, so without
	// a bound a wedged token endpoint would hang the LLM call indefinitely.
	credentialResolveTimeout = 30 * time.Second

	// credentialExpiryEnvSuffix names the variable carrying a token's expiry:
	// ANTHROPIC_AUTH_TOKEN -> ANTHROPIC_AUTH_TOKEN_EXPIRES_AT. The value is RFC
	// 3339 UTC and is the true expiry with no safety margin baked in (the
	// margin is applied here, at check time, so it can be tuned without
	// rewriting what the client persisted). Absent, empty or unparseable means
	// "unknown".
	credentialExpiryEnvSuffix = "_EXPIRES_AT"
)

// Credential is a provider credential as observed at one point in time.
type Credential struct {
	// Token authenticates the request, as a bearer token.
	Token string

	// ExpiresAt is when the provider stops accepting Token, with no safety
	// margin applied. Zero means the supplier didn't say — a plain API key, or
	// a client that predates the expiry contract — in which case the cache
	// falls back to credentialRefreshTTL instead of assuming a lifetime.
	ExpiresAt time.Time
}

// credentialResolver fetches the current credential from its origin. For a
// subscription OAuth token that origin is the client's session, so a
// resolution is an RPC that also gives the client's refresher hook its chance
// to run.
type credentialResolver func(ctx context.Context) (Credential, error)

// CredentialSource is an endpoint's live credential: it resolves the current
// one on demand and caches it until shortly before it expires.
//
// Concurrent resolutions collapse onto one — parallel agents share an endpoint
// and a streaming turn issues bursts — and a resolution that fails while a
// previously resolved credential is in hand serves that one rather than
// failing the request: a client that is momentarily unreachable (a nested
// client whose parent session is mid-reconnect) is transient, and the token we
// already hold is very likely still valid.
type CredentialSource struct {
	resolve credentialResolver

	mu        sync.Mutex
	cached    Credential
	goodUntil time.Time

	// now is this source's clock. Tests replace it to exercise the expiry
	// horizon without sleeping through it.
	now func() time.Time
}

// newCredentialSource wraps a resolver in the cache. A nil resolver yields a
// nil source, so callers can pass an unconfigured credential slot through
// unconditionally: a nil source resolves to the empty credential and leaves
// the transport chain untouched.
func newCredentialSource(resolve credentialResolver) *CredentialSource {
	if resolve == nil {
		return nil
	}
	return &CredentialSource{resolve: resolve, now: time.Now}
}

// Credential returns the credential to authenticate with right now, going back
// to the origin when the cached one has reached its validity horizon.
func (src *CredentialSource) Credential(ctx context.Context) (Credential, error) {
	if src == nil {
		return Credential{}, nil
	}

	// The lock is held across the resolution on purpose: that is what makes
	// concurrent requests share one round-trip instead of stampeding the
	// client. credentialResolveTimeout bounds how long a straggler can hold it.
	src.mu.Lock()
	defer src.mu.Unlock()

	now := src.now()
	if src.cached.Token != "" && now.Before(src.goodUntil) {
		return src.cached, nil
	}

	cred, err := src.resolve(ctx)
	if err != nil {
		if src.cached.Token != "" {
			return src.cached, nil
		}
		return Credential{}, err
	}
	if cred.Token != "" {
		src.cached = cred
		src.goodUntil = credentialHorizon(now, cred)
	}
	return cred, nil
}

// Invalidate drops the cached credential, the stale-fallback copy included, so
// the next resolution goes back to the origin. Called when a provider rejects
// the credential: what we hold is known bad, and re-serving it — even as a
// fallback — would only waste the retry.
func (src *CredentialSource) Invalidate() {
	if src == nil {
		return
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	src.cached = Credential{}
	src.goodUntil = time.Time{}
}

// credentialHorizon is how long a freshly resolved credential may be reused:
// min(expiry - margin, now + maxTTL), or a short TTL when the expiry is
// unknown. A horizon already in the past — the client handed us an expired
// token — means every request re-resolves, which is exactly what gives the
// client's refresher its chance to produce a live one.
func credentialHorizon(now time.Time, cred Credential) time.Time {
	if cred.ExpiresAt.IsZero() {
		return now.Add(credentialRefreshTTL)
	}
	capped := now.Add(credentialMaxTTL)
	if safe := cred.ExpiresAt.Add(-credentialExpiryMargin); safe.Before(capped) {
		return safe
	}
	return capped
}

// credentialReloader re-runs getenv for an OAuth token variable and for the
// companion variable carrying its expiry.
//
// The two are read in that order, in one resolution, on purpose: the client's
// refresher hook fires on the *token* lookup and rewrites both variables, so
// reading the expiry first would pair a fresh token with the outgoing one's
// expiry — expiring the cache an hour early at best, and pinning a stale
// lifetime onto a new token at worst.
//
// The returned resolver carries no client identity of its own: LoadClientConfig
// binds getenv to the client whose configuration supplied the token, so a
// reload resolves against that same client. That is what lets a nested `dagger
// agent` use the session's LLM auth without ever holding credentials itself.
func credentialReloader(getenv func(context.Context, string) (string, error), tokenKey string) credentialResolver {
	return func(ctx context.Context) (Credential, error) {
		token, err := getenv(ctx, tokenKey)
		if err != nil {
			return Credential{}, fmt.Errorf("get %q: %w", tokenKey, err)
		}
		if token == "" {
			return Credential{}, nil
		}
		// An expiry we cannot read is "unknown", not "expired": the token is
		// the credential, and failing an LLM request over its metadata would
		// break every client older than the expiry contract.
		expiry, expiryErr := getenv(ctx, tokenKey+credentialExpiryEnvSuffix)
		if expiryErr != nil {
			expiry = ""
		}
		return Credential{Token: token, ExpiresAt: parseCredentialExpiry(expiry)}, nil
	}
}

// parseCredentialExpiry reads a *_EXPIRES_AT value: RFC 3339, UTC, the true
// expiry. Anything unreadable is reported as unknown rather than as expired —
// a malformed value must degrade to the TTL fallback, not turn every single
// request into a resolution (and, client-side, a refresh attempt).
func parseCredentialExpiry(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	expiresAt, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return expiresAt.UTC()
}

// detach re-bases resolution onto base, dropping the requesting context's
// cancellation.
//
// A credential is resolved from whichever request happens to need one, but the
// endpoint holding the source is memoized for the whole conversation and can
// outlive that request by hours — a detached agent loop keeps stepping long
// after the API call that routed its endpoint returned. Resolving against that
// call's context would start failing the moment it completed. loadLLMRouter
// passes a session-scoped base instead, so resolution stays alive for exactly
// as long as the client's session; the timeout keeps one hung resolution from
// hanging the LLM request behind it.
func (resolve credentialResolver) detach(base context.Context) credentialResolver {
	if resolve == nil {
		return nil
	}
	return func(context.Context) (Credential, error) {
		ctx, cancel := context.WithTimeout(base, credentialResolveTimeout)
		defer cancel()
		return resolve(ctx)
	}
}

// applyCredential writes a resolved credential onto an outgoing request.
// Providers differ in more than the header name: Codex also derives a required
// account-id header from the token's JWT claims, so that has to be recomputed
// whenever the token rotates rather than pinned at client construction.
type applyCredential func(req *http.Request, token string)

func applyBearer(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
}

func applyCodexBearer(req *http.Request, token string) {
	applyBearer(req, token)
	if accountID := extractChatGPTAccountID(token); accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
}

// credentialTransport re-authenticates every request from a CredentialSource,
// overwriting whatever the SDK baked in at construction. This is the single
// choke point that makes credential freshness a property of the transport
// rather than of each provider client: every provider is built with
// option.WithHTTPClient(endpoint.otelHTTPClient(...)).
type credentialTransport struct {
	base  http.RoundTripper
	src   *CredentialSource
	apply applyCredential
}

// newCredentialTransport inserts credential handling into a transport chain,
// or returns base untouched when there is no source. Plain API keys and CI
// runs have no source and must be exactly unaffected.
func newCredentialTransport(base http.RoundTripper, src *CredentialSource, apply applyCredential) http.RoundTripper {
	if src == nil {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	if apply == nil {
		apply = applyBearer
	}
	return &credentialTransport{base: base, src: src, apply: apply}
}

func (t *credentialTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cred, err := t.src.Credential(req.Context())
	if err != nil {
		return nil, fmt.Errorf("resolve LLM credential: %w", err)
	}
	if cred.Token != "" {
		// RoundTrippers must not mutate the request they are handed.
		req = req.Clone(req.Context())
		t.apply(req, cred.Token)
	}
	// An empty resolution leaves the SDK's own header in place: the variable
	// may simply have gone missing from the client's environment, and the
	// credential the endpoint was routed with is a better guess than none.
	return t.base.RoundTrip(req)
}

// credentialApplier returns how this endpoint's provider carries its
// credential on the wire.
func (endpoint *LLMEndpoint) credentialApplier() applyCredential {
	if endpoint.Provider == OpenAICodex {
		return applyCodexBearer
	}
	return applyBearer
}

// isAuthFailure reports whether err is the provider rejecting our credential
// (HTTP 401) rather than any other failure. Both SDKs surface the status on a
// typed API error, and the Codex client preserves it through its own error
// wrapper (see codexAPIError).
func isAuthFailure(err error) bool {
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return anthropicErr.StatusCode == http.StatusUnauthorized
	}
	var openaiErr *openai.Error
	if errors.As(err, &openaiErr) {
		return openaiErr.StatusCode == http.StatusUnauthorized
	}
	return false
}

// credentialError turns a provider's rejection of our credential into
// something the user can act on. Only a subscription login gets rewritten: a
// rejected API key is a variable the user exported themselves and the
// provider's own message names it well enough, whereas an OAuth login lives in
// the CLI's config and is refreshed behind the session, so "401
// authentication_error" tells its owner nothing about what to do next.
func (endpoint *LLMEndpoint) credentialError(err error) error {
	if !endpoint.IsOAuth {
		return err
	}
	return &expiredLoginError{provider: endpoint.Provider, err: err}
}

type expiredLoginError struct {
	provider LLMProvider
	err      error
}

func (e *expiredLoginError) Error() string {
	return fmt.Sprintf("%s rejected the subscription login: it has expired or been revoked — "+
		"re-run `dagger llm setup` to log in again (%v)", e.provider, e.err)
}

func (e *expiredLoginError) Unwrap() error { return e.err }
