package main

// Fake Dagger Cloud for integration tests. It records OTLP telemetry request
// bodies under /events/<request path>.json, and doubles as the OAuth token
// endpoint: any path ending in /oauth/token exchanges the expected refresh
// token for a sequential short-lived access token, recording the issuance
// under /events/<path prefix>/issued-tokens.txt so tests can tell callers
// apart (e.g. engine vs client refreshes). Telemetry requests must
// authenticate with either the static engine token ("test", as basic auth)
// or an access token this server issued; anything else (e.g. a stale token)
// panics, killing the service and failing the test loudly. Every authorized
// telemetry request is logged to /events/requests.log as "<credential>
// <path>", so tests can pin a request to the credential that made it.
//
// Paths starting with /hang/ simulate a Cloud outage: the server accepts the
// request and then sits on it longer than any client or engine timeout, the
// worst outage mode (a hard-down endpoint at least fails fast).

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const refreshToken = "test-refresh-token"

var (
	mu     sync.Mutex
	issued = map[string]bool{}
	counts = map[string]int{}
)

func main() {
	err := http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint: gosec
		if strings.HasPrefix(r.URL.Path, "/hang/") {
			time.Sleep(60 * time.Second)
		}

		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			issueToken(w, r)
			return
		}

		credential, ok := authorized(r)
		if !ok {
			panic("invalid authorization header: " + r.Header.Get("Authorization"))
		}

		// one line per telemetry request, so tests can pin a request to the
		// credential that made it (the recorded bodies alone can't tell an
		// engine-issued token apart from a client-issued one)
		appendFile("/events/requests.log", strings.NewReader(credential+" "+r.URL.Path+"\n"))

		appendFile(filepath.Join("/events", r.URL.Path+".json"), r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	if !errors.Is(err, net.ErrClosed) {
		panic(err)
	}
}

func issueToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != refreshToken {
		http.Error(w, "unexpected token request: "+r.Form.Encode(), http.StatusBadRequest)
		return
	}

	prefix := strings.TrimSuffix(r.URL.Path, "/oauth/token")

	mu.Lock()
	counts[prefix]++
	token := fmt.Sprintf("fresh-token%s-%d", strings.ReplaceAll(prefix, "/", "-"), counts[prefix])
	issued[token] = true
	mu.Unlock()

	appendFile(filepath.Join("/events", prefix, "issued-tokens.txt"), strings.NewReader(token+"\n"))

	w.Header().Set("Content-Type", "application/json")
	// expires_in of 1s keeps the token always within oauth2's expiry delta,
	// so every export has to refresh again - exercising refresh repeatedly
	fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","refresh_token":%q,"expires_in":1}`, token, refreshToken)
}

// authorized reports whether the request carries a known credential, and
// returns the credential so callers can record who made the request.
func authorized(r *http.Request) (string, bool) {
	if basicAuth, _, ok := r.BasicAuth(); ok && basicAuth == "test" {
		return "basic:test", true
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	mu.Lock()
	defer mu.Unlock()
	return token, issued[token]
}

func appendFile(fp string, contents io.Reader) {
	if err := os.MkdirAll(filepath.Dir(fp), 0755); err != nil {
		panic(err)
	}
	f, err := os.OpenFile(fp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if _, err := io.Copy(f, contents); err != nil {
		panic(err)
	}
}
