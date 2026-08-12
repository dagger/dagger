package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// codexTestToken builds a minimal JWT-shaped token carrying the given
// chatgpt_account_id claim, matching what extractChatGPTAccountID parses.
func codexTestToken(t *testing.T, accountID string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	require.NoError(t, err)
	return "hdr." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// testClock is a hand-advanced clock, so the cache's expiry horizon can be
// walked across precisely instead of slept through.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestCredentialSource(clk *testClock, resolve credentialResolver) *CredentialSource {
	src := newCredentialSource(resolve)
	src.now = clk.Now
	return src
}

// rotatingCredential hands out a fresh token on every resolution, so a test
// can tell which resolution a request's header came from.
func rotatingCredential(expiresAt time.Time) (credentialResolver, *atomic.Int64) {
	var n atomic.Int64
	return func(context.Context) (Credential, error) {
		return Credential{
			Token:     fmt.Sprintf("token-%d", n.Add(1)),
			ExpiresAt: expiresAt,
		}, nil
	}, &n
}

// TestCredentialTransportRefreshesPerRequest is the point of the whole
// mechanism: a provider SDK client bakes its credential in at construction, so
// the only way a rotated token reaches the wire without rebuilding the client
// is for the transport to re-authenticate each request.
func TestCredentialTransportRefreshesPerRequest(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	clk := newTestClock()
	resolve, _ := rotatingCredential(time.Time{})
	endpoint := &LLMEndpoint{
		Provider: Anthropic,
		IsOAuth:  true,
		// The stale snapshot the SDK would have pinned.
		AuthToken:       "token-0",
		AuthTokenSource: newTestCredentialSource(clk, resolve),
	}
	client := endpoint.otelHTTPClient("anthropic")

	for range 3 {
		req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		// What the SDK baked in at construction time.
		req.Header.Set("Authorization", "Bearer token-0")
		resp, err := client.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		// Outlive the cache, so each request really does re-resolve.
		clk.Advance(credentialRefreshTTL + time.Second)
	}

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{
		"Bearer token-1",
		"Bearer token-2",
		"Bearer token-3",
	}, seen)
}

// TestCredentialTransportLeavesRequestUnmodified locks in the RoundTripper
// contract: the request handed to RoundTrip must not be mutated (the SDK may
// reuse it across its own retries).
func TestCredentialTransportLeavesRequestUnmodified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	rt := newCredentialTransport(nil, newCredentialSource(func(context.Context) (Credential, error) {
		return Credential{Token: "fresh"}, nil
	}), applyBearer)

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer stale")
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	resp.Body.Close()

	require.Equal(t, "Bearer stale", req.Header.Get("Authorization"))
}

// TestCredentialTransportNoSourceIsPassthrough covers the plain-API-key path
// (`dagger call` in CI): no source, no interception, no behavior change.
func TestCredentialTransportNoSourceIsPassthrough(t *testing.T) {
	base := http.DefaultTransport
	require.Equal(t, base, newCredentialTransport(base, nil, applyBearer))
	require.Nil(t, newCredentialTransport(nil, nil, applyBearer))
	// An unconfigured credential slot passes through the whole chain as nil,
	// so routing a plain API key builds exactly the transport it always did.
	require.Nil(t, newCredentialSource(nil))
	ep := &LLMEndpoint{Provider: Anthropic, Key: "sk-plain"}
	require.Nil(t, ep.AuthTokenSource)
	cred, err := ep.AuthTokenSource.Credential(t.Context())
	require.NoError(t, err)
	require.Empty(t, cred.Token)
}

// TestCredentialSourceCollapsesBursts checks the cache: a streaming turn that
// issues several requests back-to-back resolves the credential once, and
// concurrent requests (parallel agents sharing an endpoint) single-flight onto
// one round-trip rather than stampeding the client.
func TestCredentialSourceCollapsesBursts(t *testing.T) {
	clk := newTestClock()
	var calls atomic.Int64
	release := make(chan struct{})
	src := newTestCredentialSource(clk, func(context.Context) (Credential, error) {
		calls.Add(1)
		<-release
		return Credential{Token: "tok"}, nil
	})

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cred, err := src.Credential(t.Context())
			require.NoError(t, err)
			require.Equal(t, "tok", cred.Token)
		}()
	}
	close(release)
	wg.Wait()
	require.Equal(t, int64(1), calls.Load())

	for range 5 {
		cred, err := src.Credential(t.Context())
		require.NoError(t, err)
		require.Equal(t, "tok", cred.Token)
	}
	require.Equal(t, int64(1), calls.Load())
}

// TestCredentialSourceExpiresAtHorizon walks the cache across the expiry the
// client reported: a credential is replaced one margin *before* it dies, and
// not a moment earlier.
func TestCredentialSourceExpiresAtHorizon(t *testing.T) {
	clk := newTestClock()
	expiresAt := clk.Now().Add(2 * time.Minute)
	resolve, calls := rotatingCredential(expiresAt)
	src := newTestCredentialSource(clk, resolve)

	cred, err := src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "token-1", cred.Token)
	require.Equal(t, expiresAt, cred.ExpiresAt)

	// Right up to the margin, the cached credential stands — this is the
	// property that keeps a busy conversation off the client's back.
	clk.Advance(2*time.Minute - credentialExpiryMargin - time.Second)
	cred, err = src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "token-1", cred.Token)
	require.Equal(t, int64(1), calls.Load())

	// One margin before the true expiry, it is replaced.
	clk.Advance(2 * time.Second)
	cred, err = src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "token-2", cred.Token)
}

// TestCredentialSourceMaxTTL: expiry is not the only reason a token changes —
// a re-login rotates it early — so a long-lived credential is still re-read
// periodically.
func TestCredentialSourceMaxTTL(t *testing.T) {
	clk := newTestClock()
	resolve, _ := rotatingCredential(clk.Now().Add(time.Hour))
	src := newTestCredentialSource(clk, resolve)

	cred, err := src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "token-1", cred.Token)

	clk.Advance(credentialMaxTTL - time.Second)
	cred, err = src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "token-1", cred.Token)

	clk.Advance(2 * time.Second)
	cred, err = src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "token-2", cred.Token)
}

// TestCredentialSourceUnknownExpiryFallsBackToTTL covers a client that says
// nothing about expiry — one older than the *_EXPIRES_AT contract, or an
// OAuth response that omitted expires_in. Unknown must mean "re-read soon",
// never "expired" (which would re-resolve, and client-side re-refresh, on
// every single request).
func TestCredentialSourceUnknownExpiryFallsBackToTTL(t *testing.T) {
	clk := newTestClock()
	resolve, _ := rotatingCredential(time.Time{})
	src := newTestCredentialSource(clk, resolve)

	cred, err := src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "token-1", cred.Token)

	clk.Advance(credentialRefreshTTL - time.Second)
	cred, err = src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "token-1", cred.Token)

	clk.Advance(2 * time.Second)
	cred, err = src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "token-2", cred.Token)
}

// TestParseCredentialExpiry pins the wire format the client half exports:
// RFC 3339, UTC, the true expiry with no margin baked in. Anything else is
// unknown — a garbage value must not read as "expired".
func TestParseCredentialExpiry(t *testing.T) {
	require.Equal(t,
		time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC),
		parseCredentialExpiry("2026-01-02T15:04:05Z"))
	// An offset is still RFC 3339; normalize it to UTC.
	require.Equal(t,
		time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC),
		parseCredentialExpiry("2026-01-02T16:04:05+01:00"))
	require.True(t, parseCredentialExpiry(" \n").IsZero())
	require.True(t, parseCredentialExpiry("").IsZero())
	require.True(t, parseCredentialExpiry("1767366245").IsZero(), "unix seconds are not the contract")
	require.True(t, parseCredentialExpiry("not a timestamp").IsZero())
}

// TestCredentialSourceFallsBackToStale: a transient failure to reach the
// client (a nested session mid-reconnect) must not fail the LLM request when a
// previously resolved token is still in hand.
func TestCredentialSourceFallsBackToStale(t *testing.T) {
	clk := newTestClock()
	var fail atomic.Bool
	src := newTestCredentialSource(clk, func(context.Context) (Credential, error) {
		if fail.Load() {
			return Credential{}, context.DeadlineExceeded
		}
		return Credential{Token: "tok"}, nil
	})
	cred, err := src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "tok", cred.Token)

	fail.Store(true)
	clk.Advance(credentialRefreshTTL + time.Second)
	cred, err = src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "tok", cred.Token)

	// With nothing cached, the failure is the caller's problem.
	empty := newTestCredentialSource(clk, func(context.Context) (Credential, error) {
		return Credential{}, context.DeadlineExceeded
	})
	_, err = empty.Credential(t.Context())
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestCredentialSourceInvalidate is the entry point 401 recovery uses: after a
// provider rejects the credential, the cached copy must be gone — including as
// the stale fallback, since re-serving a token the provider just refused would
// waste the one retry that recovery gets.
func TestCredentialSourceInvalidate(t *testing.T) {
	clk := newTestClock()
	var fail atomic.Bool
	resolve, calls := rotatingCredential(time.Time{})
	src := newTestCredentialSource(clk, func(ctx context.Context) (Credential, error) {
		if fail.Load() {
			return Credential{}, context.DeadlineExceeded
		}
		return resolve(ctx)
	})

	cred, err := src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "token-1", cred.Token)

	src.Invalidate()
	cred, err = src.Credential(t.Context())
	require.NoError(t, err)
	require.Equal(t, "token-2", cred.Token, "invalidation re-resolves inside the TTL")
	require.Equal(t, int64(2), calls.Load())

	src.Invalidate()
	fail.Store(true)
	_, err = src.Credential(t.Context())
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"an invalidated credential must not come back as the stale fallback")
}

// TestCredentialReloaderReadsTokenBeforeExpiry pins the ordering the two
// halves agreed on: the client's refresher hook fires on the *token* lookup
// and rewrites both variables, so reading the expiry first would pair a fresh
// token with the outgoing one's expiry.
func TestCredentialReloaderReadsTokenBeforeExpiry(t *testing.T) {
	var order []string
	token, expiry := "token-v1", "2026-01-02T15:04:05Z"
	getenv := func(_ context.Context, key string) (string, error) {
		order = append(order, key)
		switch key {
		case "ANTHROPIC_AUTH_TOKEN":
			// The refresher hook runs inside this lookup and updates both.
			token, expiry = "token-v2", "2026-01-02T16:04:05Z"
			return token, nil
		case "ANTHROPIC_AUTH_TOKEN_EXPIRES_AT":
			return expiry, nil
		}
		return "", nil
	}

	cred, err := credentialReloader(getenv, "ANTHROPIC_AUTH_TOKEN")(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN_EXPIRES_AT"}, order)
	require.Equal(t, "token-v2", cred.Token)
	require.Equal(t, time.Date(2026, 1, 2, 16, 4, 5, 0, time.UTC), cred.ExpiresAt)
}

func TestCredentialReloaderTolerance(t *testing.T) {
	t.Run("no token means no credential and no expiry lookup", func(t *testing.T) {
		var lookups []string
		cred, err := credentialReloader(func(_ context.Context, key string) (string, error) {
			lookups = append(lookups, key)
			return "", nil
		}, "OPENAI_CODEX_AUTH_TOKEN")(t.Context())
		require.NoError(t, err)
		require.Empty(t, cred.Token)
		require.Equal(t, []string{"OPENAI_CODEX_AUTH_TOKEN"}, lookups)
	})

	t.Run("unreadable expiry is unknown, not fatal", func(t *testing.T) {
		cred, err := credentialReloader(func(_ context.Context, key string) (string, error) {
			if key == "ANTHROPIC_AUTH_TOKEN" {
				return "tok", nil
			}
			return "", fmt.Errorf("client went away")
		}, "ANTHROPIC_AUTH_TOKEN")(t.Context())
		require.NoError(t, err)
		require.Equal(t, "tok", cred.Token)
		require.True(t, cred.ExpiresAt.IsZero())
	})

	t.Run("garbage expiry is unknown, not expired", func(t *testing.T) {
		cred, err := credentialReloader(func(_ context.Context, key string) (string, error) {
			if key == "ANTHROPIC_AUTH_TOKEN" {
				return "tok", nil
			}
			return "sometime next week", nil
		}, "ANTHROPIC_AUTH_TOKEN")(t.Context())
		require.NoError(t, err)
		require.True(t, cred.ExpiresAt.IsZero())

		// Unknown expiry caches for the TTL, rather than re-resolving forever.
		clk := newTestClock()
		require.Equal(t, clk.Now().Add(credentialRefreshTTL), credentialHorizon(clk.Now(), cred))
	})

	t.Run("failed token lookup is fatal", func(t *testing.T) {
		_, err := credentialReloader(func(context.Context, string) (string, error) {
			return "", fmt.Errorf("client went away")
		}, "ANTHROPIC_AUTH_TOKEN")(t.Context())
		require.ErrorContains(t, err, "ANTHROPIC_AUTH_TOKEN")
	})
}

// TestCredentialResolverDetach: resolution must survive the request that
// happened to trigger it — the endpoint holding the resolver outlives that
// request by the whole conversation — while still dying with the session it
// was scoped to.
func TestCredentialResolverDetach(t *testing.T) {
	var nilResolver credentialResolver
	require.Nil(t, nilResolver.detach(t.Context()))

	sessionCtx, closeSession := context.WithCancel(t.Context())
	defer closeSession()

	var hadDeadline bool
	resolve := credentialResolver(func(ctx context.Context) (Credential, error) {
		if err := ctx.Err(); err != nil {
			return Credential{}, err
		}
		_, hadDeadline = ctx.Deadline()
		return Credential{Token: "tok"}, nil
	}).detach(sessionCtx)

	// The request that asks for the credential may already be over.
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	cancelRequest()
	cred, err := resolve(requestCtx)
	require.NoError(t, err)
	require.Equal(t, "tok", cred.Token)
	require.True(t, hadDeadline, "a resolution must be bounded, or a hung client hangs the LLM call")

	// The session going away does end it.
	closeSession()
	_, err = resolve(t.Context())
	require.ErrorIs(t, err, context.Canceled)
}

// TestCredentialApplierCodex checks that the Codex account-id header, which
// is derived from the token's JWT claims, follows the token when it rotates
// instead of staying pinned from client construction.
func TestCredentialApplierCodex(t *testing.T) {
	endpoint := &LLMEndpoint{Provider: OpenAICodex}
	apply := endpoint.credentialApplier()

	req, err := http.NewRequest(http.MethodGet, "https://chatgpt.com/backend-api/codex/responses", nil)
	require.NoError(t, err)
	apply(req, codexTestToken(t, "acct-abc"))
	require.Equal(t, "acct-abc", req.Header.Get("chatgpt-account-id"))

	apply(req, codexTestToken(t, "acct-xyz"))
	require.Equal(t, "acct-xyz", req.Header.Get("chatgpt-account-id"))
}
