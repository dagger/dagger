// Package inputs runs in a process of its own: a module's client keeps the
// session it first connects to.
package inputs

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"example.com/importsafe/internal/dagger"
)

var (
	mu       sync.Mutex
	response string
	requests int
	dialed   int
	hold     chan struct{} // set: each request waits for it to close
	arrived  = make(chan struct{}, 64)
)

func TestMain(m *testing.M) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		body, wait := response, hold
		mu.Unlock()
		if wait != nil {
			arrived <- struct{}{}
			<-wait
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			mu.Lock()
			dialed++
			mu.Unlock()
		}
	}
	server.Start()
	os.Setenv("DAGGER_SESSION_PORT", strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	os.Setenv("DAGGER_SESSION_TOKEN", "secret")
	code := m.Run()
	server.Close()
	os.Exit(code)
}

func serve(body string, wait chan struct{}) {
	mu.Lock()
	defer mu.Unlock()
	response, hold, requests, dialed = body, wait, 0, 0
}

// The entrypoint reads its call's inputs in one request, JSON values as
// they're sent, and a call without arguments has none.
func TestDispatchInputs(t *testing.T) {
	for _, tc := range []struct {
		name, response, parentName, fnName, parent string
		args                                       map[string]string
	}{
		{
			name:       "arguments",
			response:   `{"data":{"currentFunctionCall":{"parentName":"Example","name":"build","parent":"{\"a\":\"x \\\"y\\\" \\\\ z\"}","inputArgs":[{"name":"src","value":"\"dir-id\""},{"name":"flags","value":"[\"-v\",\"a\\\\b\"]"}]}}}`,
			parentName: "Example", fnName: "build", parent: `{"a":"x \"y\" \\ z"}`,
			args: map[string]string{"src": `"dir-id"`, "flags": `["-v","a\\b"]`},
		},
		{
			name:       "no arguments",
			response:   `{"data":{"currentFunctionCall":{"parentName":"Example","name":"version","parent":"{}","inputArgs":[]}}}`,
			parentName: "Example", fnName: "version", parent: "{}",
			args: map[string]string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serve(tc.response, nil)
			parentName, fnName, parent, args, err := dagger.Connect().CurrentFunctionCall().DispatchInputs(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			n := requests
			mu.Unlock()
			if n != 1 {
				t.Errorf("%d requests, want 1", n)
			}
			if parentName != tc.parentName || fnName != tc.fnName || string(parent) != tc.parent {
				t.Errorf("got %q %q %q, want %q %q %q", parentName, fnName, parent, tc.parentName, tc.fnName, tc.parent)
			}
			got := map[string]string{}
			for k, v := range args {
				got[k] = string(v)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.args)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("arguments %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

// Concurrent requests reuse the session's connections rather than dialing
// new ones: a second burst as wide as the first opens none.
func TestConcurrentRequestsReuseConnections(t *testing.T) {
	const width = 16
	ctr := dagger.Connect().Container()
	serve(`{"data":{"container":{"platform":"linux/amd64"}}}`, nil)
	// A first request, so the client's connection is set up.
	if _, err := ctr.Platform(context.Background()); err != nil {
		t.Fatal(err)
	}
	total := 0
	for burst := range 2 {
		wait := make(chan struct{})
		serve(`{"data":{"container":{"platform":"linux/amd64"}}}`, wait)
		var wg sync.WaitGroup
		for range width {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := ctr.Platform(context.Background()); err != nil {
					t.Errorf("burst %d: %v", burst, err)
				}
			}()
		}
		for range width {
			<-arrived
		}
		close(wait)
		wg.Wait()
		mu.Lock()
		if burst == 1 && dialed != 0 {
			t.Errorf("a second burst of %d requests dialed %d new connections, want 0", width, dialed)
		}
		total += dialed
		mu.Unlock()
	}
	if total > width {
		t.Errorf("%d connections for two bursts of %d", total, width)
	}
}
