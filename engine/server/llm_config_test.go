package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestLiveLLMConfig(t *testing.T) {
	sess := &daggerSession{sessionID: "session", mainClientCallerID: "main"}
	sess.state.Store(sessionStateInitialized)
	newClient := func(id string, parents ...string) *clientRuntime {
		return &clientRuntime{clientRecord: &clientRecord{
			clientID: id, daggerSession: sess,
			clientMetadata:  &engine.ClientMetadata{ClientID: id, SessionID: sess.sessionID},
			parentClientIDs: parents, metadataSealed: true, accepting: true,
		}, state: clientStateInitialized, lifecycleLeases: make(map[uint64]clientLifecycleLeaseRecord)}
	}
	main := newClient("main")
	nested := newClient("nested", "main")
	main.clientMetadata.LLMConfig = &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{
		"anthropic": {APIKey: "env://OLD_KEY"},
	}}
	sess.clientRuntimes = map[string]*clientRuntime{"main": main, "nested": nested}
	installTestClientRecords(sess)
	srv := &Server{daggerSessions: map[string]*daggerSession{sess.sessionID: sess}}
	clientContext := func(client *clientRuntime) context.Context {
		scope, err := sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "LLM config test")
		require.NoError(t, err)
		t.Cleanup(scope.Lease().Release)
		ctx, err := engine.ContextWithClientScope(t.Context(), scope)
		require.NoError(t, err)
		return ctx
	}
	mainCtx, nestedCtx := clientContext(main), clientContext(nested)
	request := func(ctx context.Context, client *clientRuntime, method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, engine.LLMConfigEndpoint, strings.NewReader(body)).WithContext(ctx)
		require.NoError(t, srv.serveLLMConfig(rec, req, client))
		return rec
	}
	require.Equal(t, "true\n", request(nestedCtx, nested, http.MethodGet, "").Body.String())
	_, _, before, err := srv.LLMConfigMetadata(nestedCtx)
	require.NoError(t, err)
	request(mainCtx, main, http.MethodPut, `{"providers":{"openai":{"api_key":"env://NEW_KEY"},"unused":null}}`)
	mainMD, parentMD, after, err := srv.LLMConfigMetadata(nestedCtx)
	require.NoError(t, err)
	require.NotEqual(t, before, after)
	require.Nil(t, mainMD.LLMConfig.Providers["anthropic"])
	require.Equal(t, "env://NEW_KEY", mainMD.LLMConfig.Providers["openai"].APIKey)
	require.Nil(t, parentMD.LLMConfig)
	require.Equal(t, "env://OLD_KEY", main.clientMetadata.LLMConfig.Providers["anthropic"].APIKey)

	// Snapshots cannot mutate the next read or reintroduce removed providers.
	mainMD.LLMConfig.Providers["openai"].APIKey = "changed"
	mainMD, _, _, err = srv.LLMConfigMetadata(nestedCtx)
	require.NoError(t, err)
	require.Equal(t, "env://NEW_KEY", mainMD.LLMConfig.Providers["openai"].APIKey)
	request(mainCtx, main, http.MethodPut, `null`)
	require.Equal(t, "false\n", request(nestedCtx, nested, http.MethodGet, "").Body.String())

	// A nested update belongs to that client, not to the session's main client.
	request(nestedCtx, nested, http.MethodPut, `{"providers":{"local":{"base_url":"http://localhost:1234"}}}`)
	require.Equal(t, "true\n", request(nestedCtx, nested, http.MethodGet, "").Body.String())
	require.Equal(t, "false\n", request(mainCtx, main, http.MethodGet, "").Body.String())

	// Configuration snapshots and replacements share the session lock.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 20; j++ {
				sess.replaceLLMConfig(main.clientRecord, nil)
				_, _, _, err := srv.LLMConfigMetadata(nestedCtx)
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
}
