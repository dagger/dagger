package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecHTTPHandlerRegistryOwnedBySession(t *testing.T) {
	t.Parallel()

	srv, sess, _, ctx, requestScope := newNestedTransportTestFixture(t)
	defer requestScope.Lease().Release()
	sess.execHTTPHandlers = newExecHTTPHandlerRegistry()

	registry, err := srv.ExecHTTPHandlers(ctx)
	require.NoError(t, err)
	require.Same(t, sess.execHTTPHandlers, registry)
}

func TestExecHTTPHandlerRegistryTokensAreOpaqueAndUnique(t *testing.T) {
	t.Parallel()

	registry := newExecHTTPHandlerRegistry()
	tokenPattern := regexp.MustCompile(`^[0-9a-z]{25}$`)
	seen := map[string]struct{}{}
	for range 512 {
		token, unregister := registry.Register(http.NotFoundHandler())
		require.Regexp(t, tokenPattern, token)
		_, duplicate := seen[token]
		require.False(t, duplicate, "duplicate capability token %q", token)
		seen[token] = struct{}{}
		t.Cleanup(unregister)
	}
}

func TestExecHTTPHandlerRegistryDispatchesExactToken(t *testing.T) {
	t.Parallel()

	registry := newExecHTTPHandlerRegistry()
	token, _ := registry.Register(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "/_dagger/exec-http", request.URL.Path)
		response.Header().Set("X-Exec-Handler", "reached")
		response.WriteHeader(http.StatusAccepted)
		_, _ = response.Write([]byte("handled"))
	}))

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://dagger/_dagger/exec-http", nil)
	registry.ServeHTTP(token, response, request)
	require.Equal(t, http.StatusAccepted, response.Code)
	require.Equal(t, "reached", response.Header().Get("X-Exec-Handler"))
	require.Equal(t, "handled", response.Body.String())

	for _, invalid := range []string{"", token + "x", token[:len(token)-1]} {
		response := httptest.NewRecorder()
		registry.ServeHTTP(invalid, response, request)
		require.Equal(t, http.StatusNotFound, response.Code)
	}
}

func TestExecHTTPHandlerRegistryUnregisterIsIdempotent(t *testing.T) {
	t.Parallel()

	registry := newExecHTTPHandlerRegistry()
	token, unregister := registry.Register(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "http://dagger/", nil)

	response := httptest.NewRecorder()
	registry.ServeHTTP(token, response, request)
	require.Equal(t, http.StatusNoContent, response.Code)

	unregister()
	unregister()

	response = httptest.NewRecorder()
	registry.ServeHTTP(token, response, request)
	require.Equal(t, http.StatusNotFound, response.Code)
}

func TestExecHTTPHandlerRegistryConcurrentAccess(t *testing.T) {
	t.Parallel()

	registry := newExecHTTPHandlerRegistry()
	var tokens sync.Map
	errs := make(chan error, 32*64*2)
	var wait sync.WaitGroup
	for worker := range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := range 64 {
				body := fmt.Sprintf("%d/%d", worker, iteration)
				token, unregister := registry.Register(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
					_, _ = response.Write([]byte(body))
				}))
				if _, duplicate := tokens.LoadOrStore(token, struct{}{}); duplicate {
					errs <- fmt.Errorf("duplicate concurrent capability token %q", token)
				}

				response := httptest.NewRecorder()
				registry.ServeHTTP(token, response, httptest.NewRequest(http.MethodGet, "http://dagger/", nil))
				if response.Body.String() != body {
					errs <- fmt.Errorf("handler %s returned %q", body, response.Body.String())
				}

				unregister()
				unregister()
			}
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestExecHTTPHandlerRegistrySessionTeardown(t *testing.T) {
	t.Parallel()

	srv := newTeardownTestServer(t)
	sess, _ := newTeardownTestSession(srv, "session", "main", 0)
	sess.execHTTPHandlers = newExecHTTPHandlerRegistry()
	token, unregister := sess.execHTTPHandlers.Register(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))

	response := httptest.NewRecorder()
	srv.ServeExecHTTP(sess.sessionID, token, response, httptest.NewRequest(http.MethodGet, "http://dagger/", nil))
	require.Equal(t, http.StatusNoContent, response.Code)

	releaseTeardownDrain(sess)
	require.NoError(t, srv.removeDaggerSession(t.Context(), sess))
	unregister()
	unregister()

	response = httptest.NewRecorder()
	srv.ServeExecHTTP(sess.sessionID, token, response, httptest.NewRequest(http.MethodGet, "http://dagger/", nil))
	require.Equal(t, http.StatusNotFound, response.Code)

	closedToken, closedUnregister := sess.execHTTPHandlers.Register(http.NotFoundHandler())
	require.Empty(t, closedToken)
	closedUnregister()
}

func TestServeExecHTTPRequiresSession(t *testing.T) {
	t.Parallel()

	srv := &Server{daggerSessions: map[string]*daggerSession{}}
	request := httptest.NewRequest(http.MethodGet, "http://dagger/", nil)

	response := httptest.NewRecorder()
	srv.ServeExecHTTP("", "token", response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code)

	response = httptest.NewRecorder()
	srv.ServeExecHTTP("unknown", "token", response, request)
	require.Equal(t, http.StatusNotFound, response.Code)
}
