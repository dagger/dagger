package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// llmEndpointTestServer is the minimal Query.Server an LLM.Endpoint()
// resolution needs: a dagql server to run the `secret(uri:){plaintext}`
// lookups against, and one client identity to load config from (so
// loadLLMRouter's main-client/parent-client layering collapses to a single
// load).
type llmEndpointTestServer struct {
	*mockServer
	srv *dagql.Server
	md  *engine.ClientMetadata
}

func (s *llmEndpointTestServer) Server(context.Context) (*dagql.Server, error) {
	return s.srv, nil
}

func (s *llmEndpointTestServer) MainClientCallerMetadata(context.Context) (*engine.ClientMetadata, error) {
	return s.md, nil
}

func (s *llmEndpointTestServer) NonModuleParentClientMetadata(context.Context) (*engine.ClientMetadata, error) {
	return s.md, nil
}

// llmEnv is a mutable stand-in for the client's environment, so a test can
// rotate ANTHROPIC_AUTH_TOKEN mid-"session" the way the CLI's env refresher
// does when the OAuth access token expires.
type llmEnv struct {
	mu   sync.Mutex
	vars map[string]string
	// reads counts env:// lookups, i.e. how often the engine actually went
	// back to the client for a value.
	reads map[string]int
}

func (e *llmEnv) get(uri string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reads[uri]++
	return e.vars[uri]
}

func (e *llmEnv) set(uri, val string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vars[uri] = val
}

func (e *llmEnv) readCount(uri string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reads[uri]
}

// newLLMEndpointTestCtx wires a context in which (*LLM).Endpoint resolves for
// real: it routes through loadLLMRouter -> LoadClientConfig -> the mocked
// `secret(uri:){plaintext}` selection, reading from the returned llmEnv.
func newLLMEndpointTestCtx(t *testing.T) (context.Context, *llmEnv) {
	t.Helper()

	env := &llmEnv{
		vars: map[string]string{
			"file://.env":                "",
			"env://ANTHROPIC_AUTH_TOKEN": "token-v1",
			"env://ANTHROPIC_MODEL":      "claude-sonnet-4-5",
		},
		reads: map[string]int{},
	}

	// Mirror the real secret schema's cache semantics (core/schema/secret.go):
	// `secret(uri:)` is PerCallInput and `plaintext` is DoNotCache, so every
	// resolution really does go back to the client — which is what the CLI's
	// env refresher hook relies on.
	srv := newCoreDagqlServerForTest(t, LLMTestQuery{})
	dagql.Fields[LLMTestQuery]{
		dagql.Func("secret", func(_ context.Context, _ LLMTestQuery, args struct {
			URI string
		}) (mockSecret, error) {
			return mockSecret{uri: args.URI}, nil
		}).WithInput(dagql.PerCallInput),
	}.Install(srv)
	dagql.Fields[mockSecret]{
		dagql.Func("plaintext", func(_ context.Context, self mockSecret, _ struct{}) (string, error) {
			return env.get(self.uri), nil
		}).DoNotCache("plaintext is read fresh from the client"),
	}.Install(srv)

	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)

	md := &engine.ClientMetadata{ClientID: "llm-test-client", SessionID: "llm-test-session"}
	query := &Query{Server: &llmEndpointTestServer{
		mockServer: &mockServer{clientMetadata: md},
		srv:        srv,
		md:         md,
	}}

	ctx := dagql.ContextWithCache(engine.ContextWithClientMetadata(t.Context(), md), cache)
	return ContextWithQuery(ctx, query), env
}

// anthropic401Server is a stand-in provider that rejects everything, recording
// the credential each request carried. A 401 body is all the SDK needs to
// produce a typed API error, and it keeps the tests off the streaming happy
// path — what is under test is which token went out, not what came back.
func anthropic401Server(t *testing.T, onRequest func(auth string)) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		onRequest(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		//nolint:errcheck
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired"}}`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func llmTestHistory() []*LLMMessage {
	return []*LLMMessage{{
		Role:    LLMMessageRoleUser,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hi"}},
	}}
}

// TestLLMEndpointResolvesCredentialPerRequest is the core of the fix. The
// endpoint stays memoized — re-routing costs a full config load, ~21 variables
// at two client round-trips each — but the credential is no longer part of
// what gets memoized: the endpoint's transport asks its source per request, so
// a token the client rotated mid-session reaches the provider without anything
// rebuilding the endpoint or the SDK client.
func TestLLMEndpointResolvesCredentialPerRequest(t *testing.T) {
	ctx, env := newLLMEndpointTestCtx(t)
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)

	var mu sync.Mutex
	var seen []string
	ts := anthropic401Server(t, func(auth string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, auth)
	})
	env.set("env://ANTHROPIC_BASE_URL", ts.URL)

	llm, err := query.NewLLM(ctx, "claude-sonnet-4-5", "")
	require.NoError(t, err)

	ep, err := llm.Endpoint(ctx)
	require.NoError(t, err)
	require.Equal(t, Anthropic, ep.Provider)
	require.True(t, ep.IsOAuth)
	require.NotNil(t, ep.AuthTokenSource)
	assert.Equal(t, "token-v1", ep.AuthToken, "the snapshot observed at routing time")

	clk := newTestClock()
	ep.AuthTokenSource.now = clk.Now

	_, err = ep.Client.SendQuery(ctx, llmTestHistory(), nil, &LLMCallOpts{})
	require.Error(t, err)

	// The client refreshes the expired access token (secretprovider's
	// EnvRefresher hook + os.Setenv, engine/client/secretprovider/env.go).
	env.set("env://ANTHROPIC_AUTH_TOKEN", "token-v2")
	clk.Advance(credentialRefreshTTL + time.Second)

	_, err = ep.Client.SendQuery(ctx, llmTestHistory(), nil, &LLMCallOpts{})
	require.Error(t, err)

	mu.Lock()
	require.Equal(t, []string{"Bearer token-v1", "Bearer token-v2"}, seen)
	mu.Unlock()

	// The memo is intact — that is deliberate, and no longer costs freshness.
	again, err := llm.Endpoint(ctx)
	require.NoError(t, err)
	assert.Same(t, ep, again)
	assert.Equal(t, "token-v1", again.AuthToken,
		"the routing-time snapshot is unchanged; only the transport is live")

	// Clone() copies the endpoint pointer, and every LLM transition —
	// withPrompt, withResponse, withToolResult, step, loop, fork — goes
	// through it, so the whole conversation shares the one live source.
	t.Run("clones share the live credential", func(t *testing.T) {
		next := llm.WithPrompt("hello").
			WithResponse([]*LLMContentBlock{{Kind: LLMContentText, Text: "hi"}}, LLMTokenUsage{}).
			WithToolResult("call_1", "ok", false).
			Clone()
		epNext, err := next.Endpoint(ctx)
		require.NoError(t, err)
		assert.Same(t, ep, epNext)

		env.set("env://ANTHROPIC_AUTH_TOKEN", "token-v3")
		clk.Advance(credentialRefreshTTL + time.Second)
		_, err = epNext.Client.SendQuery(ctx, llmTestHistory(), nil, &LLMCallOpts{})
		require.Error(t, err)

		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, "Bearer token-v3", seen[len(seen)-1])
	})

	// withModel/withReasoningEffort still drop the memo (the route itself
	// changes), and re-derive against the current environment.
	t.Run("withModel re-routes", func(t *testing.T) {
		ep, err := llm.WithModel("claude-sonnet-4-5", "").Endpoint(ctx)
		require.NoError(t, err)
		assert.Equal(t, "token-v3", ep.AuthToken)
	})
}

// TestLLMEndpointHonorsReportedExpiry runs the *_EXPIRES_AT contract
// end-to-end: the client exports the token's true expiry beside it, and the
// engine holds the credential until one margin before that instant — far past
// the blind 30s TTL, which is all it can do when the expiry is unknown.
func TestLLMEndpointHonorsReportedExpiry(t *testing.T) {
	ctx, env := newLLMEndpointTestCtx(t)
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)

	var mu sync.Mutex
	var seen []string
	ts := anthropic401Server(t, func(auth string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, auth)
	})
	env.set("env://ANTHROPIC_BASE_URL", ts.URL)

	// The login has three minutes left on it: short of credentialMaxTTL, so
	// the reported expiry is what governs the horizon here.
	clk := newTestClock()
	const lifetime = 3 * time.Minute
	env.set("env://ANTHROPIC_AUTH_TOKEN_EXPIRES_AT",
		clk.Now().Add(lifetime).Format(time.RFC3339))

	llm, err := query.NewLLM(ctx, "claude-sonnet-4-5", "")
	require.NoError(t, err)
	ep, err := llm.Endpoint(ctx)
	require.NoError(t, err)
	ep.AuthTokenSource.now = clk.Now

	send := func() {
		_, err := ep.Client.SendQuery(ctx, llmTestHistory(), nil, &LLMCallOpts{})
		require.Error(t, err)
	}
	send()

	// A rotation nobody announced stays invisible until the cached credential
	// reaches its horizon, which is a whole margin before the expiry and many
	// TTLs after the resolution.
	env.set("env://ANTHROPIC_AUTH_TOKEN", "token-v2")
	clk.Advance(lifetime - credentialExpiryMargin - time.Second)
	send()
	clk.Advance(2 * time.Second)
	send()

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{
		"Bearer token-v1",
		"Bearer token-v1",
		"Bearer token-v2",
	}, seen)

	reads := env.readCount("env://ANTHROPIC_AUTH_TOKEN_EXPIRES_AT")
	assert.Positive(t, reads, "the expiry must be read alongside the token")
}

// TestLLMAuthFailureRetriesOnceWithFreshToken covers 401 recovery: the token
// died mid-conversation, the client already has its replacement, and the turn
// should survive. Exactly one retry — a login that is genuinely revoked must
// fail fast with something the user can act on, not spin the backoff for two
// minutes and then report "401 authentication_error".
func TestLLMAuthFailureRetriesOnceWithFreshToken(t *testing.T) {
	ctx, env := newLLMEndpointTestCtx(t)
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)

	var mu sync.Mutex
	var seen []string
	ts := anthropic401Server(t, func(auth string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, auth)
		// The CLI refreshed in the background; the engine is still holding
		// the token it cached before that happened.
		env.set("env://ANTHROPIC_AUTH_TOKEN", "token-v2")
	})
	env.set("env://ANTHROPIC_BASE_URL", ts.URL)

	llm, err := query.NewLLM(ctx, "claude-sonnet-4-5", "")
	require.NoError(t, err)

	_, err = llm.sendQueryWithRetry(ctx, llmTestHistory(), nil, "", 0)
	require.Error(t, err)
	assert.ErrorContains(t, err, "dagger llm setup",
		"an expired subscription login must say what to do about it")
	assert.ErrorContains(t, err, string(Anthropic))

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"Bearer token-v1", "Bearer token-v2"}, seen,
		"the retry must carry the re-resolved token, and there must be only one")
}

// TestLLMAuthFailureWithoutSourceIsPermanent: with no credential source there
// is nothing to re-resolve, so a 401 fails immediately — an API-key setup and
// CI behave exactly as before.
func TestLLMAuthFailureWithoutSourceIsPermanent(t *testing.T) {
	var mu sync.Mutex
	var requests int
	ts := anthropic401Server(t, func(string) {
		mu.Lock()
		defer mu.Unlock()
		requests++
	})

	endpoint := &LLMEndpoint{
		Model:    "claude-sonnet-4-5",
		BaseURL:  ts.URL,
		Provider: Anthropic,
		Key:      "sk-plain",
	}
	endpoint.Client = newAnthropicClient(endpoint)

	llm := &LLM{endpoint: endpoint, endpointMtx: &sync.Mutex{}, mcp: newMCP()}
	_, err := llm.sendQueryWithRetry(t.Context(), llmTestHistory(), nil, "", 0)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "dagger llm setup",
		"an API key is the user's own env var; don't send them to the OAuth flow")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, requests)
}

// TestAnthropicClientWithoutSourceKeepsBakedToken is the invariant behind the
// whole design: an SDK client captures its credential at construction, so an
// endpoint without a source sends the token it was routed with forever, no
// matter what is assigned to LLMEndpoint.AuthToken afterwards.
func TestAnthropicClientWithoutSourceKeepsBakedToken(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	ts := anthropic401Server(t, func(auth string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, auth)
	})

	endpoint := &LLMEndpoint{
		Model:     "claude-sonnet-4-5",
		BaseURL:   ts.URL,
		Provider:  Anthropic,
		AuthToken: "token-v1",
		IsOAuth:   true,
	}
	client := newAnthropicClient(endpoint)

	_, err := client.SendQuery(t.Context(), llmTestHistory(), nil, &LLMCallOpts{})
	require.Error(t, err)
	assert.True(t, isAuthFailure(err), "a 401 must be recognizable as such: %v", err)
	assert.False(t, client.IsRetryable(err),
		"resending is only worth it once the credential has been re-resolved")

	endpoint.AuthToken = "token-v2"
	_, err = client.SendQuery(t.Context(), llmTestHistory(), nil, &LLMCallOpts{})
	require.Error(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 2)
	assert.Equal(t, "Bearer token-v1", seen[0])
	assert.Equal(t, "Bearer token-v1", seen[1],
		"the bearer token is captured at client construction, not read per request")
}

// TestCodexClientRotatesAccountIDWithToken is the Codex (ChatGPT subscription)
// twin. Codex needs more than the header swapped: the required
// chatgpt-account-id header is derived from the token's own JWT claims, so it
// has to be recomputed whenever the token rotates.
func TestCodexClientRotatesAccountIDWithToken(t *testing.T) {
	type request struct{ auth, account string }
	var mu sync.Mutex
	var seen []request
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, request{
			auth:    r.Header.Get("Authorization"),
			account: r.Header.Get("chatgpt-account-id"),
		})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		//nolint:errcheck
		w.Write([]byte(`{"detail":"Your authentication token has expired."}`))
	}))
	t.Cleanup(ts.Close)

	tokenV1 := codexTestToken(t, "acct-old")
	tokenV2 := codexTestToken(t, "acct-new")

	var current atomic.Pointer[string]
	current.Store(&tokenV1)
	clk := newTestClock()
	endpoint := &LLMEndpoint{
		Model:     "gpt-5.5",
		BaseURL:   ts.URL,
		Provider:  OpenAICodex,
		AuthToken: tokenV1,
		IsOAuth:   true,
		AuthTokenSource: newTestCredentialSource(clk, func(context.Context) (Credential, error) {
			return Credential{Token: *current.Load()}, nil
		}),
	}
	client := newOpenAICodexClient(endpoint)

	_, err := client.SendQuery(t.Context(), llmTestHistory(), nil, &LLMCallOpts{})
	require.Error(t, err)
	assert.True(t, isAuthFailure(err),
		"the Codex error wrapper must keep the status classifiable: %v", err)

	current.Store(&tokenV2)
	clk.Advance(credentialRefreshTTL + time.Second)
	_, err = client.SendQuery(t.Context(), llmTestHistory(), nil, &LLMCallOpts{})
	require.Error(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 2)
	assert.Equal(t, "Bearer "+tokenV1, seen[0].auth)
	assert.Equal(t, "acct-old", seen[0].account)
	assert.Equal(t, "Bearer "+tokenV2, seen[1].auth)
	assert.Equal(t, "acct-new", seen[1].account,
		"the account id is derived from the token, so it must rotate with it")
}

// TestLLMCredentialResolvesAgainstLoadingClient guards the property that lets
// a nested `dagger agent` use the session's LLM auth without ever holding
// credentials: the reload runs against the client whose configuration supplied
// the token, whoever happens to be making the request.
func TestLLMCredentialResolvesAgainstLoadingClient(t *testing.T) {
	srv := newCoreDagqlServerForTest(t, LLMTestQuery{})
	dagql.Fields[LLMTestQuery]{
		dagql.Func("secret", func(_ context.Context, _ LLMTestQuery, args struct {
			URI string
		}) (mockSecret, error) {
			return mockSecret{uri: args.URI}, nil
		}).WithInput(dagql.PerCallInput),
	}.Install(srv)
	dagql.Fields[mockSecret]{
		dagql.Func("plaintext", func(ctx context.Context, self mockSecret, _ struct{}) (string, error) {
			md, err := engine.ClientMetadataFromContext(ctx)
			if err != nil {
				return "", err
			}
			if self.uri == "env://ANTHROPIC_AUTH_TOKEN" {
				return "token-of-" + md.ClientID, nil
			}
			return "", nil
		}).DoNotCache("plaintext is read fresh from the client"),
	}.Install(srv)

	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	withClient := func(id string) context.Context {
		return dagql.ContextWithCache(engine.ContextWithClientMetadata(t.Context(),
			&engine.ClientMetadata{ClientID: id, SessionID: "llm-test-session"}), cache)
	}

	router := new(LLMRouter)
	_, err = router.LoadClientConfig(withClient("host"), srv)
	require.NoError(t, err)
	require.Equal(t, "token-of-host", router.AnthropicAuthToken)
	require.NotNil(t, router.reloadAnthropicAuthToken)

	// The request that needs the credential comes from a nested client, which
	// has no login of its own. Resolution must still land on the host's.
	cred, err := router.reloadAnthropicAuthToken(withClient("nested-agent"))
	require.NoError(t, err)
	assert.Equal(t, "token-of-host", cred.Token)
}

// TestLLMEndpointConcurrentCloneAndResolve exercises the Clone()/Endpoint()
// pair the way a session actually does: a detached agent loop cloning per step
// while the CLI's status line resolves the model on the same persistent node.
// Worth little without -race, and worth a lot with it.
func TestLLMEndpointConcurrentCloneAndResolve(t *testing.T) {
	ctx, _ := newLLMEndpointTestCtx(t)
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)

	llm, err := query.NewLLM(ctx, "claude-sonnet-4-5", "")
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := llm.Endpoint(ctx)
			assert.NoError(t, err)
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			clone := llm.Clone().WithPrompt("hi")
			_, err := clone.Endpoint(ctx)
			assert.NoError(t, err)
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = llm.WithModel("claude-sonnet-4-5", "")
		}()
	}
	wg.Wait()
}
