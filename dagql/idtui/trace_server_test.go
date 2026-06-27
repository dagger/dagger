package idtui

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"go.opentelemetry.io/otel/trace"
)

// TestServeTrace is the interactive trace console: it holds one live trace
// session in memory and exposes it over HTTP, so the real interactive TUI can be
// driven headlessly — GET the screen, POST a keystroke, GET the network stats —
// the way a human at a terminal would, just pty-less and curl-able (e.g. by an
// LLM across tool calls). State accumulates across requests like a real session,
// and spans/logs are fetched lazily on demand (the point of the harness), so a
// big trace starts instantly and /network shows exactly what each expand pulled.
//
// Opt-in: set DRIVE_SERVE=<addr> and DRIVE_TRACE_ID=<id> (with DAGGER_CLOUD_URL /
// auth). DRIVE_VERBOSITY / DRIVE_WIDTH / DRIVE_HEIGHT tune the view. Use
// -timeout 0 so go test doesn't kill the server.
//
//	cd cloud/dagger
//	env DRIVE_SERVE=:7777 DRIVE_TRACE_ID=<id> DAGGER_CLOUD_URL=http://localhost:8020 \
//	    go test ./dagql/idtui/ -run TestServeTrace -count=1 -timeout 0 &
//	curl -s localhost:7777/screen
//	curl -s --data 'right'      localhost:7777/key   # expand focused span
//	curl -s --data 'down down'  localhost:7777/key   # navigate
//	curl -s 'localhost:7777/spans?q=load workspace'  # find a loaded span
//	curl -s --data '<spanHex>'  localhost:7777/zoom  # jump to it
//	curl -s localhost:7777/network
//
// Endpoints (text/plain; screens are ANSI-stripped unless ?raw=1):
//
//	GET  /screen        the current frame
//	POST /key           body = key script (tuist.ParseKey names); apply, return frame
//	POST /zoom          body = span hex; zoom to it, return frame
//	GET  /network       fetch stats (the --debug numbers)
//	GET  /spans[?q=sub] loaded-span id/status/name listing (to find a span to zoom)
//	GET  /help          this list
func TestServeTrace(t *testing.T) {
	addr := os.Getenv("DRIVE_SERVE")
	traceID := os.Getenv("DRIVE_TRACE_ID")
	if addr == "" || traceID == "" {
		t.Skip("set DRIVE_SERVE=<addr> and DRIVE_TRACE_ID=<id> to serve a trace console")
	}
	sess := newTraceSession(t, newLiveSource(t, traceID), driveConfigure(t))
	if w, h := os.Getenv("DRIVE_WIDTH"), os.Getenv("DRIVE_HEIGHT"); w != "" || h != "" {
		sess.Resize(atoiOr(t, w, 120), atoiOr(t, h, 40))
	}

	// The session is single-goroutine (no event loop); serialize all access.
	var mu sync.Mutex

	screen := func(w http.ResponseWriter, r *http.Request, frame string) {
		if r.URL.Query().Get("raw") == "" {
			frame = ansi.Strip(frame)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, frame)
	}
	reqBody := func(r *http.Request) string {
		b, _ := io.ReadAll(r.Body)
		return strings.TrimSpace(string(b))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/screen", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		screen(w, r, sess.Render())
	})
	mux.HandleFunc("/key", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		frame := sess.Render()
		for _, k := range parseDriveKeys(reqBody(r)) {
			frame = sess.Press(k)
		}
		screen(w, r, frame)
	})
	mux.HandleFunc("/zoom", func(w http.ResponseWriter, r *http.Request) {
		hex := reqBody(r)
		sid, err := trace.SpanIDFromHex(hex)
		if err != nil {
			http.Error(w, fmt.Sprintf("bad span hex %q: %v", hex, err), http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		screen(w, r, sess.Zoom(dagui.SpanID{SpanID: sid}))
	})
	mux.HandleFunc("/network", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, networkSummary(sess.Network()))
	})
	mux.HandleFunc("/spans", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, sess.loadedSpans(r.URL.Query().Get("q")))
	})
	help := "trace console — endpoints:\n" +
		"  GET  /screen        current frame (?raw=1 keeps ANSI)\n" +
		"  POST /key   <keys>   apply key script, return frame\n" +
		"  POST /zoom  <hex>    zoom to a span, return frame\n" +
		"  GET  /network        fetch stats\n" +
		"  GET  /spans[?q=sub]  loaded-span id/status/name listing\n" +
		"  GET  /help           this list\n" +
		"keys: ←↑↓→ move · right/l expand · left/h collapse · enter zoom · " +
		"r error origin · L logs · +/- verbosity · / search\n"
	helpHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, help)
	}
	mux.HandleFunc("/help", helpHandler)
	mux.HandleFunc("/", helpHandler)

	fmt.Printf("trace console: %s on %s\n%s", traceID, addr, help)
	if err := http.ListenAndServe(addr, mux); err != nil {
		t.Fatalf("serve %s: %v", addr, err)
	}
}

// loadedSpans lists the spans currently in the frontend's DB (everything fetched
// so far), optionally filtered by a name substring, so a console user can find a
// span hex to zoom to.
func (s *traceSession) loadedSpans(q string) string {
	var b strings.Builder
	for _, sp := range s.fe.db.Spans.Order {
		if q != "" && !strings.Contains(sp.Name, q) {
			continue
		}
		status := "ok"
		switch {
		case sp.IsFailed():
			status = "ERROR"
		case sp.IsRunning():
			status = "run"
		}
		fmt.Fprintf(&b, "%s  %-5s  %s\n", sp.ID, status, sp.Name)
	}
	return b.String()
}

// driveConfigure returns a session configurator honoring DRIVE_VERBOSITY.
func driveConfigure(t *testing.T) func(*dagui.FrontendOpts) {
	t.Helper()
	return func(opts *dagui.FrontendOpts) {
		if v := os.Getenv("DRIVE_VERBOSITY"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				t.Fatalf("DRIVE_VERBOSITY=%q: %v", v, err)
			}
			opts.Verbosity = n
		}
	}
}

// networkSummary renders the fetch counters the way `dagger trace --debug` does.
func networkSummary(st *fetchStats) string {
	su, sl := st.op(opSpanUpdates), st.op(opSpanLogs)
	return fmt.Sprintf(
		"GetSpanUpdates: %d reqs / %d records / %d bytes\n"+
			"GetSpanLogs:    %d reqs / %d records / %d bytes\n"+
			"log spans fetched: %d\n",
		su.Requests, su.Records, su.Bytes,
		sl.Requests, sl.Records, sl.Bytes,
		len(st.logRequests))
}

// parseDriveKeys splits a key script into individual key specs. Tokens are
// whitespace- or comma-separated; a "key*N" token repeats that key N times.
func parseDriveKeys(script string) []string {
	fields := strings.FieldsFunc(script, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ','
	})
	var keys []string
	for _, f := range fields {
		key, count := f, 1
		if star := strings.LastIndex(f, "*"); star > 0 {
			if n, err := strconv.Atoi(f[star+1:]); err == nil && n > 0 {
				key, count = f[:star], n
			}
		}
		for range count {
			keys = append(keys, key)
		}
	}
	return keys
}

func atoiOr(t *testing.T, s string, fallback int) int {
	t.Helper()
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("expected an integer, got %q: %v", s, err)
	}
	return n
}
