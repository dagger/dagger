package schema

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// Exercise credential reloads through the installed secret schema, its session
// resource bindings, and the client's gRPC attachable. In particular, route the
// endpoint inside a dagql call that completes before credentials rotate.
func TestLLMCredentialReloadAfterRoutingCall(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested=%t", nested), func(t *testing.T) {
			main := &engine.ClientMetadata{SessionID: "llm-auth-session", ClientID: "main"}
			parent := main
			if nested {
				parent = &engine.ClientMetadata{SessionID: main.SessionID, ClientID: "nested"}
			}
			var mu sync.Mutex
			var seen []string
			providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, r.Header.Get("Authorization"))
				mu.Unlock()
				if r.Header.Get("Authorization") == "Bearer token-v3" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
						"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
						"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
						"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
						"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
						"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"access token rejected"}}`))
			}))
			t.Cleanup(providerServer.Close)
			env := &llmAuthSecrets{vars: map[string]string{
				"env://ANTHROPIC_AUTH_TOKEN": "token-v1",
				"env://ANTHROPIC_BASE_URL":   providerServer.URL,
			}}
			srv := &llmAuthSchemaServer{
				currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: main},
				parent:                    parent,
				attachables: map[string]*grpc.ClientConn{
					main.ClientID: newLLMAuthAttachable(t, env),
				},
			}
			if nested {
				srv.attachables[parent.ClientID] = newLLMAuthAttachable(t, &llmAuthSecrets{})
			}
			ctx := engine.ContextWithClientMetadata(t.Context(), parent)
			cache, err := dagql.NewCache(ctx, "", nil, nil)
			require.NoError(t, err)
			ctx = dagql.ContextWithCache(ctx, cache)
			root := core.NewRoot(srv)
			ctx = core.ContextWithQuery(ctx, root)
			base, err := NewCoreSchemaBase(ctx, srv)
			require.NoError(t, err)
			dag, err := base.Fork(ctx, root, "")
			require.NoError(t, err)
			srv.dag = dag

			routingCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			var llm dagql.ObjectResult[*core.LLM]
			err = dag.Select(routingCtx, dag.Root(), &llm, dagql.Selector{
				Field: "llm",
				Args:  []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.NewString("claude-sonnet-4-5"))}},
			})
			require.NoError(t, err)
			var provider string
			err = dag.Select(routingCtx, llm, &provider, dagql.Selector{Field: "provider"})
			require.NoError(t, err)
			require.Equal(t, "anthropic", provider)
			cancel()

			// Endpoint is already memoized by provider(), so this must retain
			// its original source rather than accidentally re-routing the LLM.
			ep, err := llm.Self().Endpoint(ctx)
			require.NoError(t, err)
			require.Equal(t, "token-v1", ep.AuthToken)
			require.NotNil(t, ep.AuthTokenSource)
			cred, err := ep.AuthTokenSource.Credential(ctx)
			require.NoError(t, err)
			require.Equal(t, "token-v1", cred.Token)

			env.set("env://ANTHROPIC_AUTH_TOKEN", "token-v2")
			ep.AuthTokenSource.Invalidate()
			cred, err = ep.AuthTokenSource.Credential(ctx)
			require.NoError(t, err)
			require.Equal(t, "token-v2", cred.Token)
			require.Equal(t, "token-v1", ep.AuthToken)

			// A 401 must reach the client's refresher even when time-based
			// refresh would keep returning the old token. The reloader is
			// detached from routingCtx, but must preserve the rejection hint.
			env.mu.Lock()
			env.refreshRejected = true
			env.mu.Unlock()
			for attempt := range 2 {
				res, err := ep.Client.SendQuery(ctx, []*core.LLMMessage{{
					Role:    core.LLMMessageRoleUser,
					Content: []*core.LLMContentBlock{{Kind: core.LLMContentText, Text: "hi"}},
				}}, nil, &core.LLMCallOpts{})
				if attempt == 0 {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.Equal(t, "hello", res.TextContent())
				}
			}
			mu.Lock()
			require.Equal(t, []string{"Bearer token-v2", "Bearer token-v3"}, seen)
			mu.Unlock()
			env.mu.Lock()
			require.Equal(t, fmt.Sprintf("%x", sha256.Sum256([]byte("token-v2"))), env.lastRejected)
			env.mu.Unlock()

			// Once the provider has rejected the token, failed client lookups
			// must remain errors, not turn into an empty token and resurrect
			// the old SDK credential on the next HTTP request.
			env.mu.Lock()
			env.err = fmt.Errorf("client credential lookup unavailable")
			env.mu.Unlock()
			ep.AuthTokenSource.Invalidate()
			_, err = ep.AuthTokenSource.Credential(ctx)
			require.ErrorContains(t, err, "client credential lookup unavailable")
		})
	}
}

func TestComposedLLMCredentialIsolatedAcrossSessions(t *testing.T) {
	for _, withResource := range []bool{false, true} {
		t.Run(fmt.Sprintf("resourceDependency=%t", withResource), func(t *testing.T) {
			cache, err := dagql.NewCache(t.Context(), "", nil, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })

			var previous *core.LLM
			for _, session := range []string{"first", "second"} {
				t.Run(session, func(t *testing.T) {
					md := &engine.ClientMetadata{SessionID: session, ClientID: session + "-client"}
					var newLease func(engine.ClientLeaseKind, string) *engine.ClientLifecycleLease
					newLease = func(kind engine.ClientLeaseKind, owner string) *engine.ClientLifecycleLease {
						return engine.NewClientLifecycleLease(kind, owner, nil, func(kind engine.ClientLeaseKind, owner string) (*engine.ClientLifecycleLease, error) {
							return newLease(kind, owner), nil
						})
					}
					lease := newLease(engine.ClientLeaseRequest, "llm-auth-test")
					t.Cleanup(lease.Release)
					scope, err := engine.NewClientScope(md, lease)
					require.NoError(t, err)
					ctx, err := engine.ContextWithClientScope(t.Context(), scope)
					require.NoError(t, err)
					ctx = dagql.ContextWithCache(ctx, cache)
					env := &llmAuthSecrets{vars: map[string]string{
						"env://ANTHROPIC_AUTH_TOKEN": "token-" + session,
						"env://ANTHROPIC_MODEL":      "claude-sonnet-4-5",
					}}
					srv := &llmAuthSchemaServer{
						currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: md},
						parent:                    md,
						attachables:               map[string]*grpc.ClientConn{md.ClientID: newLLMAuthAttachable(t, env)},
					}
					root := core.NewRoot(srv)
					ctx = core.ContextWithQuery(ctx, root)
					base, err := NewCoreSchemaBase(ctx, srv)
					require.NoError(t, err)
					dag, err := base.Fork(ctx, root, "")
					require.NoError(t, err)
					srv.dag = dag
					srv.deps = core.NewSchemaBuilder(root, nil)
					dagql.Fields[*core.Query]{
						dagql.Func("authTestAgents", func(context.Context, *core.Query, struct{}) (*core.AgentMiddlewareGroup, error) {
							return &core.AgentMiddlewareGroup{}, nil
						}),
					}.Install(dag)

					// Compose without a base on a session-independent group. Its
					// default llm() re-selects the same pinned model and provider in
					// both sessions, but must not alias their live endpoint objects.
					var llm dagql.ObjectResult[*core.LLM]
					require.NoError(t, dag.Select(ctx, dag.Root(), &llm,
						dagql.Selector{Field: "authTestAgents"}, dagql.Selector{Field: "compose"}))
					if withResource && previous != nil {
						// The miss produced a genuinely fresh LLM. A later load must
						// not canonicalize it to the previous session's endpoint.
						require.NotSame(t, previous, llm.Self())
					}
					var model string
					require.NoError(t, dag.Select(ctx, llm, &model, dagql.Selector{Field: "model"}))
					require.Equal(t, "claude-sonnet-4-5", model)
					llmID, err := llm.ID()
					require.NoError(t, err)
					llm, err = dagql.NewID[*core.LLM](llmID).Load(ctx, dag)
					require.NoError(t, err)
					if previous != nil && previous == llm.Self() {
						t.Error("compose reused an LLM from the previous session")
					}
					ep, err := llm.Self().Endpoint(ctx)
					require.NoError(t, err)
					// Force resolution instead of letting the short credential TTL
					// temporarily hide a wrongly reused endpoint's session binding.
					ep.AuthTokenSource.Invalidate()
					cred, err := ep.AuthTokenSource.Credential(ctx)
					require.NoError(t, err)
					require.Equal(t, "token-"+session, cred.Token)
					previous = llm.Self()
					if withResource {
						// Model a middleware result retaining a secret. On the next
						// session's compose lookup this handle is unavailable, forcing
						// execution rather than a direct cache hit. Default routing
						// loads the same model secret and makes the old result eligible
						// again before compose publishes its returned-result alias.
						var secret dagql.ObjectResult[*core.Secret]
						require.NoError(t, dag.Select(ctx, dag.Root(), &secret, dagql.Selector{
							Field: "secret",
							Args:  []dagql.NamedInput{{Name: "uri", Value: dagql.NewString("env://ANTHROPIC_MODEL")}},
						}))
						require.NoError(t, cache.AddExplicitDependency(ctx, llm, secret, "test compose resource dependency"))
					}
				})
			}
		})
	}
}

type llmAuthSchemaServer struct {
	*currentTypeDefsTestServer
	parent      *engine.ClientMetadata
	attachables map[string]*grpc.ClientConn
}

func (s *llmAuthSchemaServer) NonModuleParentClientMetadata(context.Context) (*engine.ClientMetadata, error) {
	return s.parent, nil
}

func (s *llmAuthSchemaServer) SpecificClientAttachableConn(_ context.Context, clientID string, _ core.SpecificClientAttachableConnOpts) (*grpc.ClientConn, bool, error) {
	conn, ok := s.attachables[clientID]
	return conn, ok, nil
}

type llmAuthSecrets struct {
	secrets.UnimplementedSecretsServer
	mu              sync.Mutex
	vars            map[string]string
	err             error
	refreshRejected bool
	lastRejected    string
}

func (s *llmAuthSecrets) GetSecret(ctx context.Context, req *secrets.GetSecretRequest) (*secrets.GetSecretResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	if s.refreshRejected && req.ID == "env://ANTHROPIC_AUTH_TOKEN" {
		if rejected := secretprovider.RejectedEnvValue(ctx); rejected != "" {
			s.lastRejected = rejected
			if rejected == fmt.Sprintf("%x", sha256.Sum256([]byte(s.vars[req.ID]))) {
				s.vars[req.ID] = "token-v3"
			}
		}
	}
	return &secrets.GetSecretResponse{Data: []byte(s.vars[req.ID])}, nil
}

func (s *llmAuthSecrets) set(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vars[name] = value
}

func newLLMAuthAttachable(t *testing.T, env *llmAuthSecrets) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	secrets.RegisterSecretsServer(server, env)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:llm-auth",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	})
	return conn
}
