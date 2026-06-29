package idtui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/vito/tuist"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Console (DAGGER_TUI_CONSOLE=<addr>) exposes the live pretty TUI over HTTP
// instead of a terminal, so it can be driven headlessly — GET the screen, POST a
// keystroke — the way a person at the terminal would, just pty-less and
// curl-able (handy for debugging, scripting, or an LLM operating the UI). It
// runs the command's real work in the background; telemetry arrives through the
// usual exporters and each request drains it with Step before rendering. It's a
// dev/debug affordance: off by default, and the server should be bound to
// localhost.

const (
	consoleWidth  = 120
	consoleHeight = 40
	// consoleSettleTimeout bounds how long a request keeps draining background
	// fetches (lazy span/log loads land on other goroutines) before responding.
	consoleSettleTimeout = 2 * time.Second
)

// runWithConsole runs the command's work in the background and serves the TUI
// over HTTP until the context is cancelled (e.g. SIGINT), instead of attaching
// to a terminal event loop.
func (fe *frontendPretty) runWithConsole(ctx context.Context, run func(context.Context) (cleanups.CleanupF, error)) error {
	fe.runCtx, fe.interrupt = context.WithCancelCause(ctx)
	fe.setupTUI() // focus + keymap, no event loop

	var (
		runWg  sync.WaitGroup
		runErr error
	)
	runWg.Add(1)
	go func() {
		defer runWg.Done()
		cleanup, err := run(fe.runCtx)
		if cleanup != nil {
			err = errors.Join(err, cleanup())
		}
		runErr = err
	}()

	fmt.Fprintf(os.Stderr, "dagger TUI console on http://%s (GET /screen, POST /key, GET /help)\n", fe.console)
	serveErr := fe.serveConsole(fe.runCtx)

	// Stop the background work and report its error (the console itself just
	// returning ErrServerClosed on shutdown isn't interesting).
	fe.interrupt(context.Canceled)
	runWg.Wait()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return runErr
}

// serveConsole serves the console endpoints until ctx is cancelled. All session
// access is serialized: the frontend is single-goroutine (no event loop), so a
// handler must hold the lock while it Steps and renders.
func (fe *frontendPretty) serveConsole(ctx context.Context) error {
	var mu sync.Mutex

	writeScreen := func(w http.ResponseWriter, r *http.Request, frame string) {
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
		writeScreen(w, r, fe.consoleSettle())
	})
	mux.HandleFunc("/key", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		for _, k := range parseConsoleKeys(reqBody(r)) {
			fe.tui.Inject(tuist.ParseKey(k))
		}
		writeScreen(w, r, fe.consoleSettle())
	})
	mux.HandleFunc("/type", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		// Type a literal string one rune at a time, as if entered at the
		// keyboard — for driving the search prompt ("/") and any other text
		// input. Unlike /key, the body is NOT tokenized: spaces and commas are
		// typed verbatim. Body is taken raw (only a trailing newline trimmed).
		raw, _ := io.ReadAll(r.Body)
		for _, ru := range strings.TrimRight(string(raw), "\n") {
			fe.tui.Inject(tuist.ParseKey(string(ru)))
		}
		writeScreen(w, r, fe.consoleSettle())
	})
	mux.HandleFunc("/zoom", func(w http.ResponseWriter, r *http.Request) {
		hex := reqBody(r)
		sid, err := oteltrace.SpanIDFromHex(hex)
		if err != nil {
			http.Error(w, fmt.Sprintf("bad span hex %q: %v", hex, err), http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		fe.ZoomToSpan(dagui.SpanID{SpanID: sid})
		writeScreen(w, r, fe.consoleSettle())
	})
	mux.HandleFunc("/resize", func(w http.ResponseWriter, r *http.Request) {
		// Body is "<cols>x<rows>" or "<cols> <rows>"; either dimension may be
		// omitted (or 0) to keep the current value, so "x12" just changes rows.
		fields := strings.FieldsFunc(reqBody(r), func(c rune) bool {
			return c == 'x' || c == 'X' || c == ' ' || c == ',' || c == '\t'
		})
		if len(fields) != 2 {
			http.Error(w, "want <cols>x<rows>", http.StatusBadRequest)
			return
		}
		cols, _ := strconv.Atoi(fields[0])
		rows, _ := strconv.Atoi(fields[1])
		mu.Lock()
		defer mu.Unlock()
		if cols <= 0 {
			cols = fe.consoleTerm.Columns()
		}
		if rows <= 0 {
			rows = fe.consoleTerm.Rows()
		}
		// Resize notifies the TUI (like SIGWINCH), and tuist's cache keys
		// height-dependent renders on ScreenHeight, so the next Step reflows to
		// the new size on its own -- no manual generation bump needed.
		fe.consoleTerm.Resize(cols, rows)
		writeScreen(w, r, fe.consoleSettle())
	})
	mux.HandleFunc("/spans", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, fe.consoleSpans(r.URL.Query().Get("q")))
	})
	mux.HandleFunc("/help", fe.consoleHelp)
	mux.HandleFunc("/", fe.consoleHelp)

	srv := &http.Server{Addr: fe.console, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Warn("console shutdown", "err", err)
		}
	}()
	return srv.ListenAndServe()
}

// consoleSettle Steps the TUI (draining dispatched telemetry and injected keys,
// then rendering) and keeps stepping until the frame stops changing or the
// timeout elapses — giving background lazy fetches a moment to land. Returns the
// rendered frame, clipped to the terminal viewport.
func (fe *frontendPretty) consoleSettle() string {
	lines := fe.tui.Step()
	frame := strings.Join(lines, "\n")
	deadline := time.Now().Add(consoleSettleTimeout)
	stable := 0
	for time.Now().Before(deadline) {
		// Block until any background fetches the last Step triggered (lazy
		// span/log loads via the providers -- e.g. a zoom's subtree) have
		// landed. Without this the frame settles "stable but empty" in the
		// ~80ms before a network round-trip returns, so a zoom/expand looks
		// like it surfaced nothing. The fetched spans are imported onto the
		// dispatch queue, which the next Step applies.
		if fe.fetchWaiter != nil {
			fe.fetchWaiter()
		}
		time.Sleep(40 * time.Millisecond)
		next := fe.tui.Step()
		joined := strings.Join(next, "\n")
		if joined == frame {
			stable++
			if stable >= 2 {
				break
			}
			continue
		}
		stable = 0
		lines = next
		frame = joined
	}
	return strings.Join(fe.consoleViewport(lines), "\n")
}

// consoleViewport mirrors what a real terminal actually shows: when the
// rendered frame is taller than the terminal, only the bottom Rows() lines are
// visible and the top scrolls offscreen (tuist switches to the alt screen and
// renders newLines[len-height:] — see applyFrameAltScreen). The headless
// terminal does no such clipping on its own, so without this the console would
// show content a real user could never see at that size. Reproducing the clip
// is what surfaces rendering bugs that only bite when content overflows — e.g.
// a focused row whose own promoted tests/logs push its header above the top.
func (fe *frontendPretty) consoleViewport(lines []string) []string {
	h := fe.consoleTerm.Rows()
	if h > 0 && len(lines) > h {
		return lines[len(lines)-h:]
	}
	return lines
}

// consoleSpans lists the spans currently loaded in the DB (everything fetched so
// far), optionally filtered by a name substring, so a caller can find a span hex
// to zoom to.
func (fe *frontendPretty) consoleSpans(q string) string {
	var b strings.Builder
	for _, sp := range fe.db.Spans.Order {
		if q != "" && !strings.Contains(sp.Name, q) {
			continue
		}
		// A span often passes its own OTel status through while the failure rides
		// on a link (a test/check whose error is on a descendant or linked span).
		// Surface that as FAIL so a caller can still find it by name, distinct
		// from ERROR (the span itself errored).
		status := "ok"
		switch {
		case sp.IsFailed():
			status = "ERROR"
		case sp.IsFailedOrCausedFailure():
			status = "FAIL"
		case sp.IsRunning():
			status = "run"
		}
		fmt.Fprintf(&b, "%s  %-5s  %s\n", sp.ID, status, sp.Name)
	}
	return b.String()
}

func (fe *frontendPretty) consoleHelp(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, "dagger TUI console — endpoints:\n"+
		"  GET  /screen        current frame (?raw=1 keeps ANSI)\n"+
		"  POST /key   <keys>   apply key script, return frame\n"+
		"  POST /type  <text>   type a literal string (e.g. into / search)\n"+
		"  POST /zoom  <hex>    zoom to a span, return frame\n"+
		"  POST /resize <CxR>   resize the terminal (e.g. 120x12), return frame\n"+
		"  GET  /spans[?q=sub]  loaded-span id/status/name listing\n"+
		"  GET  /help           this list\n"+
		"keys: ←↑↓→ move · right/l expand · left/h collapse · enter zoom · "+
		"r error origin · L logs · +/- verbosity · / search\n")
}

// parseConsoleKeys splits a key script into individual key specs (tuist.ParseKey
// names). Tokens are whitespace- or comma-separated; a "key*N" token repeats it.
func parseConsoleKeys(script string) []string {
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
