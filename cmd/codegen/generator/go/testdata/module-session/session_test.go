package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"example.com/importsafe/internal/dagger"
)

// Match author code that constructs the client and query during initialization.
var client = dagger.Connect()
var container = client.Container()

func TestSession(t *testing.T) {
	ctx := context.Background()
	if _, err := container.Platform(ctx); err == nil || !strings.Contains(err.Error(), "DAGGER_SESSION_PORT is not set") {
		t.Fatalf("request without a session: %v", err)
	}
	var decoded dagger.Container
	if err := json.Unmarshal([]byte("\"decoded-id\""), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ port, token, want string }{
		{"", "", "DAGGER_SESSION_PORT"},
		{"invalid", "token", "DAGGER_SESSION_PORT"},
		{"12345", "", "DAGGER_SESSION_TOKEN"},
	} {
		t.Setenv("DAGGER_SESSION_PORT", tc.port)
		t.Setenv("DAGGER_SESSION_TOKEN", tc.token)
		if _, err := container.Platform(ctx); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("missing/invalid session: %v", err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "secret" || pass != "" {
			t.Errorf("incorrect session authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{\"data\":{\"container\":{\"platform\":\"linux/amd64\"},\"node\":{\"platform\":\"linux/amd64\"}}}")
	}))
	defer server.Close()
	t.Setenv("DAGGER_SESSION_PORT", strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	t.Setenv("DAGGER_SESSION_TOKEN", "secret")
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			platform, err := container.Platform(ctx)
			if err != nil || platform != "linux/amd64" {
				t.Errorf("pre-session handle: %q, %v", platform, err)
			}
		}()
	}
	wg.Wait()
	// UnmarshalJSON uses the core package's query root, not Connect's root.
	platform, err := decoded.Platform(ctx)
	if err != nil || platform != "linux/amd64" {
		t.Fatalf("decoded handle: %q, %v", platform, err)
	}
}
