package llmconfig

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const oauthSuccessHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>Authentication successful</title>
</head>
<body>
  <p>Authentication successful. Return to your terminal to continue.</p>
</body>
</html>`

// OAuthCallbackServer listens on localhost for an OAuth callback.
type OAuthCallbackServer struct {
	server *http.Server
	code   string
	state  string
	mu     sync.Mutex
	done   chan struct{}
}

// StartOAuthCallbackServer starts a local HTTP server to receive OAuth callbacks.
// It listens on the given port and validates the state parameter.
func StartOAuthCallbackServer(port int, expectedState string) (*OAuthCallbackServer, error) {
	s := &OAuthCallbackServer{
		state: expectedState,
		done:  make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", s.handleCallback)

	s.server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}

	listener, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	go func() {
		_ = s.server.Serve(listener)
	}()

	return s, nil
}

func (s *OAuthCallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("state") != s.state {
		http.Error(w, "State mismatch", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, oauthSuccessHTML)

	s.mu.Lock()
	s.code = code
	s.mu.Unlock()

	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

// WaitForCode waits for the OAuth callback to deliver a code, or for the
// context to be cancelled. Returns empty string if timed out or cancelled.
func (s *OAuthCallbackServer) WaitForCode(ctx context.Context) string {
	timeout := time.After(60 * time.Second)
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.code
	case <-timeout:
		return ""
	case <-ctx.Done():
		return ""
	}
}

// Close shuts down the callback server.
func (s *OAuthCallbackServer) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.server.Shutdown(ctx)
}
