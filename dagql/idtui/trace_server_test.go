package idtui

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TestServeTrace runs a stateful HTTP console over one live trace session, so
// the interactive TUI can be driven incrementally — GET the screen, POST a
// keystroke, GET the network stats — instead of replaying a script from
// scratch. The session persists between requests, so navigation accumulates
// like a real interactive run. This is the ergonomic way to explore a
// misbehaving trace ad-hoc (e.g. an LLM curling it across tool calls).
//
// Opt-in: set DRIVE_SERVE=<addr> plus a source (DRIVE_FIXTURE or
// DRIVE_TRACE_ID). DRIVE_VERBOSITY / DRIVE_WIDTH / DRIVE_HEIGHT apply as in
// TestDriveTrace. Use -timeout 0 so go test doesn't kill the server.
//
//	cd cloud/dagger
//	env DRIVE_SERVE=:7777 DRIVE_FIXTURE=testdata/traces/call_loadfail.json \
//	    go test ./dagql/idtui/ -run TestServeTrace -count=1 -timeout 0 &
//	curl -s localhost:7777/screen
//	curl -s --data 'right'      localhost:7777/key
//	curl -s --data 'down down'  localhost:7777/key
//	curl -s localhost:7777/network
//
// Endpoints (text/plain; screens are ANSI-stripped unless ?raw=1):
//
//	GET  /screen        the current frame
//	POST /key           body = key script (tuist.ParseKey names); apply, return frame
//	POST /zoom          body = span hex; zoom to it, return frame
//	GET  /network       fetch stats (the --debug numbers)
//	GET  /spans[?q=sub] span id/name listing (to find a span to zoom)
//	GET  /help          this list
func TestServeTrace(t *testing.T) {
	addr := os.Getenv("DRIVE_SERVE")
	if addr == "" {
		t.Skip("set DRIVE_SERVE=<addr> (and a source) to serve a trace console")
	}
	fix, source := driveSource(t)
	sess := newTraceSession(t, fix, driveConfigure(t))
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
	body := func(r *http.Request) string {
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
		for _, k := range parseDriveKeys(body(r)) {
			frame = sess.Press(k)
		}
		screen(w, r, frame)
	})
	mux.HandleFunc("/zoom", func(w http.ResponseWriter, r *http.Request) {
		hex := body(r)
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
		q := r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, sp := range fix.Spans {
			if q != "" && !strings.Contains(sp.Name, q) {
				continue
			}
			fmt.Fprintf(w, "%s  %-7s  %s\n", sp.ID, spanStatusLabel(sp), sp.Name)
		}
	})
	help := "trace console — endpoints:\n" +
		"  GET  /screen        current frame (?raw=1 keeps ANSI)\n" +
		"  POST /key   <keys>   apply key script, return frame\n" +
		"  POST /zoom  <hex>    zoom to a span, return frame\n" +
		"  GET  /network        fetch stats\n" +
		"  GET  /spans[?q=sub]  span id/name listing\n" +
		"  GET  /help           this list\n" +
		"keys: ←↑↓→ move · right/l expand · left/h collapse · enter zoom · " +
		"r error origin · L logs · +/- verbosity · / search\n"
	helpHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, help)
	}
	mux.HandleFunc("/help", helpHandler)
	mux.HandleFunc("/", helpHandler)

	fmt.Printf("trace console: %s on %s (source=%s, %d spans)\n%s",
		fix.TraceID, addr, source, len(fix.Spans), help)
	if err := http.ListenAndServe(addr, mux); err != nil {
		t.Fatalf("serve %s: %v", addr, err)
	}
}

// spanStatusLabel summarizes a snapshot's state for the /spans listing.
func spanStatusLabel(sp dagui.SpanSnapshot) string {
	switch {
	case sp.TestStatus == dagui.TestStatusFailure:
		return "FAIL"
	case sp.TestStatus == dagui.TestStatusSuccess:
		return "pass"
	case sp.Status.Code == codes.Error:
		return "ERROR"
	default:
		return "ok"
	}
}
