package server

import (
	"context"
	"net/http"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

// execHTTPHandlerRegistry owns the HTTP handlers reachable from container
// executions in one Dagger session. Tokens are capabilities and must remain
// opaque to callers.
type execHTTPHandlerRegistry struct {
	mu       sync.RWMutex
	handlers map[string]http.Handler
	closed   bool
}

var _ core.ExecHTTPHandlerRegistry = (*execHTTPHandlerRegistry)(nil)

func newExecHTTPHandlerRegistry() *execHTTPHandlerRegistry {
	return &execHTTPHandlerRegistry{handlers: map[string]http.Handler{}}
}

func (registry *execHTTPHandlerRegistry) Register(handler http.Handler) (string, func()) {
	if handler == nil {
		panic("cannot register a nil exec HTTP handler")
	}

	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return "", func() {}
	}

	var token string
	for token == "" {
		candidate := identity.NewID()
		if _, exists := registry.handlers[candidate]; !exists {
			token = candidate
		}
	}
	registry.handlers[token] = handler
	registry.mu.Unlock()

	var once sync.Once
	return token, func() {
		once.Do(func() {
			registry.mu.Lock()
			delete(registry.handlers, token)
			registry.mu.Unlock()
		})
	}
}

func (registry *execHTTPHandlerRegistry) ServeHTTP(token string, response http.ResponseWriter, request *http.Request) {
	registry.mu.RLock()
	handler := registry.handlers[token]
	registry.mu.RUnlock()
	if handler == nil {
		http.NotFound(response, request)
		return
	}
	handler.ServeHTTP(response, request)
}

func (registry *execHTTPHandlerRegistry) Close() {
	registry.mu.Lock()
	registry.closed = true
	clear(registry.handlers)
	registry.mu.Unlock()
}

func (srv *Server) ExecHTTPHandlers(ctx context.Context) (core.ExecHTTPHandlerRegistry, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	registry := client.daggerSession.execHTTPHandlers
	if registry == nil {
		return nil, errSessionClosing
	}
	return registry, nil
}

// ServeExecHTTP forwards a request from a container-local execution listener to
// the handler capability registered in that execution's Dagger session.
func (srv *Server) ServeExecHTTP(sessionID, token string, response http.ResponseWriter, request *http.Request) {
	if sessionID == "" {
		http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	srv.daggerSessionsMu.RLock()
	sess := srv.daggerSessions[sessionID]
	srv.daggerSessionsMu.RUnlock()
	if sess == nil || sess.execHTTPHandlers == nil {
		http.NotFound(response, request)
		return
	}
	sess.execHTTPHandlers.ServeHTTP(token, response, request)
}
