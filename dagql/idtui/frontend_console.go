package idtui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"dagger.io/dagger"
	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine"
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
// usual exporters onto the dispatch queue, which a background pump (and each
// request, before rendering) drains with Step. It's a dev/debug affordance: off
// by default, and the server should be bound to localhost.

const (
	consoleWidth  = 120
	consoleHeight = 40
	// consoleSettleTimeout bounds how long a request keeps draining background
	// fetches (lazy span/log loads land on other goroutines) before responding.
	consoleSettleTimeout = 2 * time.Second
	// consoleWaitQuietDefault is how long the screen must stay unchanged for
	// /wait (without a regex) to consider it settled.
	consoleWaitQuietDefault = 2 * time.Second
	// consoleWaitTimeoutDefault/-Max bound how long a /wait request may block.
	consoleWaitTimeoutDefault = 60 * time.Second
	consoleWaitTimeoutMax     = 5 * time.Minute
	// consoleWaitPoll is how often /wait re-renders the screen while waiting.
	consoleWaitPoll = 100 * time.Millisecond
)

// runWithConsole runs the command's work in the background and serves the TUI
// over HTTP until the context is cancelled (e.g. SIGINT), instead of attaching
// to a terminal event loop.
func (fe *frontendPretty) runWithConsole(ctx context.Context, run func(context.Context) (cleanups.CleanupF, error)) error {
	fe.runCtx, fe.interrupt = context.WithCancelCause(ctx)
	// doQuit closes fe.quit (e.g. a second quit keypress injected via /key);
	// without this it would close a nil channel and panic mid-Step.
	fe.quit = make(chan struct{})
	fe.setupTUI() // focus + keymap, no event loop

	var (
		runWg      sync.WaitGroup
		runErr     error
		runCleanup cleanups.CleanupF
	)
	runWg.Add(1)
	go func() {
		defer runWg.Done()
		runCleanup, runErr = run(fe.runCtx)
	}()

	// Pump the dispatch queue in the background so dispatched work makes
	// progress between HTTP requests. The run goroutine blocks on dispatched
	// closures (SurfacedFailedCheckSpans, RequestSurfacedLogs) that otherwise
	// only run inside a request's Step -- without the pump, `dagger trace`
	// parks in its surfacing phase until someone curls the console, and a
	// SIGINT before the first request hangs forever in runWg.Wait below.
	pumpStop := make(chan struct{})
	var pumpWg sync.WaitGroup
	pumpWg.Go(func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-pumpStop:
				return
			case <-ticker.C:
				fe.consoleMu.Lock()
				fe.tui.Step()
				fe.consoleMu.Unlock()
			}
		}
	})

	fmt.Fprintf(os.Stderr, "dagger TUI console on http://%s (GET /screen, POST /key, GET /help)\n", fe.console)
	serveErr := fe.serveConsole(fe.runCtx)

	// Stop the background work and report its error (the console itself just
	// returning ErrServerClosed on shutdown isn't interesting). The pump keeps
	// draining until the run goroutine has exited: it may be parked on a
	// dispatched closure that still needs to execute.
	fe.interrupt(context.Canceled)
	runWg.Wait()
	// The console stays interactive after the initial report, just like -E.
	// Keep archive leases/connections alive for later expand/zoom requests and
	// run their cleanup before stopping the pump (cleanup may dispatch work).
	if runCleanup != nil {
		runErr = errors.Join(runErr, runCleanup())
	}
	close(pumpStop)
	pumpWg.Wait()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return runErr
}

// serveConsole serves the console endpoints until ctx is cancelled. All session
// access is serialized via fe.consoleMu: the frontend is single-goroutine (no
// event loop), so a handler must hold the lock while it Steps and renders.
func (fe *frontendPretty) serveConsole(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/screen", func(w http.ResponseWriter, r *http.Request) {
		fe.consoleMu.Lock()
		defer fe.consoleMu.Unlock()
		writeConsoleScreen(w, r, fe.consoleSettle())
	})
	mux.HandleFunc("/key", func(w http.ResponseWriter, r *http.Request) {
		keys := parseConsoleKeys(consoleRequestBody(r))
		// Validate the whole script before injecting any of it: an unknown
		// token must not leave the TUI half-driven, and must not fall through
		// tuist.ParseKey's extended-key fallback, which would *type the token
		// as literal text* into whatever is focused (e.g. an agent prompt).
		for _, k := range keys {
			if err := validateConsoleKey(k); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		fe.consoleMu.Lock()
		defer fe.consoleMu.Unlock()
		for _, k := range keys {
			fe.tui.Inject(tuist.ParseKey(k))
		}
		writeConsoleScreen(w, r, fe.consoleSettle())
	})
	mux.HandleFunc("/type", func(w http.ResponseWriter, r *http.Request) {
		fe.consoleMu.Lock()
		defer fe.consoleMu.Unlock()
		// Type a literal string one rune at a time, as if entered at the
		// keyboard — for driving the search prompt ("/") and any other text
		// input. Unlike /key, the body is NOT tokenized: spaces and commas are
		// typed verbatim. Body is taken raw (only a trailing newline trimmed).
		raw, _ := io.ReadAll(r.Body)
		for _, ru := range strings.TrimRight(string(raw), "\n") {
			fe.tui.Inject(tuist.ParseKey(string(ru)))
		}
		writeConsoleScreen(w, r, fe.consoleSettle())
	})
	mux.HandleFunc("/zoom", func(w http.ResponseWriter, r *http.Request) {
		hex := consoleRequestBody(r)
		sid, err := oteltrace.SpanIDFromHex(hex)
		if err != nil {
			http.Error(w, fmt.Sprintf("bad span hex %q: %v", hex, err), http.StatusBadRequest)
			return
		}
		fe.consoleMu.Lock()
		defer fe.consoleMu.Unlock()
		fe.ZoomToSpan(dagui.SpanID{SpanID: sid})
		writeConsoleScreen(w, r, fe.consoleSettle())
	})
	mux.HandleFunc("/resize", func(w http.ResponseWriter, r *http.Request) {
		// Body is "<cols>x<rows>" or "<cols> <rows>"; either dimension may be
		// omitted (or 0) to keep the current value, so "x12" just changes rows.
		body := consoleRequestBody(r)
		isSep := func(c rune) bool {
			return c == 'x' || c == 'X' || c == ' ' || c == ',' || c == '\t'
		}
		fields := strings.FieldsFunc(body, isSep)
		var colStr, rowStr string
		switch {
		case len(fields) == 2:
			colStr, rowStr = fields[0], fields[1]
		case len(fields) == 1 && strings.IndexFunc(body, isSep) >= 0:
			// One dimension omitted: FieldsFunc drops the empty side, so tell
			// them apart by which side of the separator the field sits on.
			if isSep(rune(body[0])) {
				rowStr = fields[0]
			} else {
				colStr = fields[0]
			}
		default:
			http.Error(w, "want <cols>x<rows>", http.StatusBadRequest)
			return
		}
		cols, _ := strconv.Atoi(colStr)
		rows, _ := strconv.Atoi(rowStr)
		fe.consoleMu.Lock()
		defer fe.consoleMu.Unlock()
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
		writeConsoleScreen(w, r, fe.consoleSettle())
	})
	mux.HandleFunc("/wait", fe.consoleWaitHandler)
	mux.HandleFunc("/spans", func(w http.ResponseWriter, r *http.Request) {
		fe.consoleMu.Lock()
		defer fe.consoleMu.Unlock()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, RenderSpanList(fe.db, r.URL.Query().Get("q"), 0))
	})
	mux.HandleFunc("/toolset", func(w http.ResponseWriter, r *http.Request) {
		// The rendered docs of the tools the model in an interactive LLM
		// session (`dagger agent`, shell prompt mode) currently sees — so QA
		// can verify the composed toolset without spending an LLM turn asking
		// the agent itself. The CLI registers the provider when a session
		// starts (SetLLMToolsProvider); a Step first drains the dispatch
		// queue so a just-registered provider is visible.
		fe.consoleMu.Lock()
		fe.tui.Step()
		provider := fe.llmToolsFn
		fe.consoleMu.Unlock()
		if provider == nil {
			http.Error(w, "no interactive LLM session (the toolset is only "+
				"available once a `dagger agent`/shell prompt session is up)",
				http.StatusNotFound)
			return
		}
		// The provider queries the engine (LLM.tools); run it without
		// consoleMu so a slow round-trip can't wedge the other endpoints.
		doc, err := provider(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("toolset: %v", err), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, doc)
	})
	mux.HandleFunc("/span", func(w http.ResponseWriter, r *http.Request) {
		hex := r.URL.Query().Get("id")
		if hex == "" {
			http.Error(w, "want ?id=<span hex>", http.StatusBadRequest)
			return
		}
		sid, err := oteltrace.SpanIDFromHex(hex)
		if err != nil {
			http.Error(w, fmt.Sprintf("bad span hex %q: %v", hex, err), http.StatusBadRequest)
			return
		}
		fe.consoleMu.Lock()
		defer fe.consoleMu.Unlock()
		detail, ok := RenderSpanDetail(fe.db, dagui.SpanID{SpanID: sid})
		if !ok {
			http.Error(w, fmt.Sprintf("unknown span %s", hex), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, detail)
	})
	mux.HandleFunc("/id", func(w http.ResponseWriter, r *http.Request) {
		dig := r.URL.Query().Get("dig")
		if dig == "" {
			http.Error(w, "want ?dig=<call digest, e.g. xxh3:...>", http.StatusBadRequest)
			return
		}
		fe.consoleMu.Lock()
		defer fe.consoleMu.Unlock()
		encoded, err := encodedIDForCallDigest(fe.db, dig)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, encoded) //nolint:gosec // G705: base64 as text/plain, never rendered as HTML.
	})
	mux.HandleFunc("/calls", func(w http.ResponseWriter, r *http.Request) {
		pattern := r.URL.Query().Get("grep")
		if pattern == "" {
			http.Error(w, "want ?grep=<regexp over rendered calls>", http.StatusBadRequest)
			return
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			http.Error(w, fmt.Sprintf("bad pattern %q: %v", pattern, err), http.StatusBadRequest)
			return
		}
		fe.consoleMu.Lock()
		defer fe.consoleMu.Unlock()
		lines := fe.db.GrepCalls(re, 200)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if len(lines) == 0 {
			io.WriteString(w, "no calls match\n")
			return
		}
		io.WriteString(w, strings.Join(lines, "\n")+"\n") //nolint:gosec // G705: call listing as text/plain, never rendered as HTML.
	})
	inspector := &consoleTraceInspector{frontend: fe}
	mux.HandleFunc("/agents", inspector.agents)
	mux.HandleFunc("/transcript", inspector.transcript)
	mux.HandleFunc("/timings", fe.consoleTimingsHandler)
	mux.HandleFunc("/help", fe.consoleHelp)
	mux.HandleFunc("/", fe.consoleHelp)

	srv := &http.Server{Addr: fe.console, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		// Give in-flight handlers a bit more than a full settle to finish.
		shutCtx, cancel := context.WithTimeout(context.Background(), consoleSettleTimeout+3*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Warn("console shutdown", "err", err)
		}
	}()
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		// ListenAndServe returns the moment Shutdown closes the listener, but
		// handlers may still be mid-Step. Wait for Shutdown to finish draining
		// them so the caller's final render doesn't race a handler's TUI access.
		<-shutdownDone
	}
	return err
}

func writeConsoleScreen(w http.ResponseWriter, r *http.Request, frame string) {
	if r.URL.Query().Get("raw") == "" {
		frame = ansi.Strip(frame)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, frame)
}

func consoleRequestBody(r *http.Request) string {
	b, _ := io.ReadAll(r.Body)
	return strings.TrimSpace(string(b))
}

func (fe *frontendPretty) consoleWaitHandler(w http.ResponseWriter, r *http.Request) {
	// Block until the screen reaches a state, then respond like /screen —
	// so QA scripts wait for a span to finish (or a prompt to appear) in
	// one request instead of polling /screen in a loop. The regex rides in
	// the body (like /key and /type take theirs) so it needs no URL
	// encoding; the durations are simple enough for query params.
	//
	// With a regex: return as soon as the ANSI-stripped screen matches it.
	// Without one: return once the screen has been unchanged for the quiet
	// duration (?quiet=, default 2s). Quiet is not an exit condition while
	// a regex is pending — an already-idle screen would end the wait
	// immediately and defeat the match. Either way the wait gives up at
	// ?timeout= (default 60s, capped) and returns the screen as it stands:
	// inspect the result, don't assume the condition was reached.
	var matchRe *regexp.Regexp
	if pattern := consoleRequestBody(r); pattern != "" {
		var err error
		matchRe, err = regexp.Compile(pattern)
		if err != nil {
			http.Error(w, fmt.Sprintf("bad match regexp %q: %v", pattern, err), http.StatusBadRequest)
			return
		}
	}
	quiet, err := consoleDuration(r.URL.Query().Get("quiet"), consoleWaitQuietDefault)
	if err != nil {
		http.Error(w, fmt.Sprintf("bad quiet param: %v", err), http.StatusBadRequest)
		return
	}
	timeout, err := consoleDuration(r.URL.Query().Get("timeout"), consoleWaitTimeoutDefault)
	if err != nil {
		http.Error(w, fmt.Sprintf("bad timeout param: %v", err), http.StatusBadRequest)
		return
	}
	timeout = min(timeout, consoleWaitTimeoutMax)

	// Poll with single Steps, holding consoleMu only per render so the
	// other endpoints (and the background pump) stay responsive for the
	// whole — potentially minutes-long — wait.
	render := func() string {
		fe.consoleMu.Lock()
		defer fe.consoleMu.Unlock()
		return ansi.Strip(strings.Join(fe.consoleViewport(fe.tui.Step()), "\n"))
	}
	deadline := time.Now().Add(timeout)
	frame := render()
	quietSince := time.Now()
	for {
		if matchRe != nil {
			if matchRe.MatchString(frame) {
				break
			}
		} else if time.Since(quietSince) >= quiet {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(consoleWaitPoll):
		}
		next := render()
		if next != frame {
			frame = next
			quietSince = time.Now()
		}
	}
	fe.consoleMu.Lock()
	defer fe.consoleMu.Unlock()
	writeConsoleScreen(w, r, fe.consoleSettle())
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
		fe.waitFetches(deadline)
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

// waitFetches runs the fetch waiter but gives up at the deadline: the caller
// holds consoleMu, so an unbounded wait on a stalled fetch would wedge every
// console endpoint, not just this request. On timeout the spawned goroutine
// keeps waiting harmlessly in the background (fetches are bound to the run
// context and unwind on interrupt).
func (fe *frontendPretty) waitFetches(deadline time.Time) {
	if fe.fetchWaiter == nil {
		return
	}
	waited := make(chan struct{})
	go func() {
		defer close(waited)
		fe.fetchWaiter()
	}()
	select {
	case <-waited:
	case <-time.After(time.Until(deadline)):
	}
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

func (fe *frontendPretty) consoleTimingsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hex := q.Get("root")
	sid, err := oteltrace.SpanIDFromHex(hex)
	if err != nil {
		http.Error(w, "want ?root=<span hex>: "+err.Error(), http.StatusBadRequest)
		return
	}
	var minDuration time.Duration
	if raw := q.Get("minDuration"); raw != "" {
		minDuration, err = time.ParseDuration(raw)
		if err != nil || minDuration < 0 {
			http.Error(w, "minDuration must be a non-negative duration (e.g. 10ms)", http.StatusBadRequest)
			return
		}
	}
	limit := 200
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 0 {
			http.Error(w, "limit must be a non-negative integer (0 = unlimited)", http.StatusBadRequest)
			return
		}
	}
	fe.consoleMu.Lock()
	defer fe.consoleMu.Unlock()
	detail, ok := RenderSpanTimings(fe.db, dagui.SpanID{SpanID: sid}, minDuration, limit, time.Now())
	if !ok {
		http.Error(w, fmt.Sprintf("span %s is not loaded", hex), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, detail)
}

func (fe *frontendPretty) consoleHelp(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, "dagger TUI console — endpoints:\n"+
		"  GET  /screen        current frame (?raw=1 keeps ANSI)\n"+
		"  POST /key   <keys>   apply key script, return frame\n"+
		"  POST /type  <text>   type a literal string (e.g. into / search)\n"+
		"  POST /zoom  <hex>    zoom to a span, return frame\n"+
		"  POST /resize <CxR>   resize the terminal (e.g. 120x12), return frame\n"+
		"  POST /wait [regex]   block until the screen matches the body regex, or\n"+
		"                       (with no body) has been unchanged for ?quiet= (default 2s);\n"+
		"                       either way return the frame at ?timeout= (default 60s) at the latest\n"+
		"  GET  /spans[?q=sub]  loaded-span id/status/name listing\n"+
		"  GET  /span?id=<hex>  span detail: status, error, timing, flags, parent chain, direct children\n"+
		"  GET  /timings?root=<hex>[&minDuration=10ms&limit=200]  loaded subtree wall timings (0 limit = unlimited)\n"+
		"  GET  /id?dig=<dig>   encoded dagql ID rebuilt for a call digest\n"+
		"  GET  /calls?grep=<re> content-search ingested call payloads (full search, bounded previews)\n"+
		"  GET  /toolset        the interactive LLM session's tool docs, when one is live\n"+
		"  GET  /agents         trace-derived roster (handles, names, state, parent, spans)\n"+
		"  GET  /transcript?agent=<handle|name>[&role=user&tool=...&grep=...&offset=0&limit=20]\n"+
		"                      recorded checkpoint data for dagger trace; idle runtime snapshots in engine sessions\n"+
		"  GET  /help           this list\n"+
		"keys: ←↑↓→ move · right/l expand · left/h collapse · enter zoom · "+
		"r error origin · L logs · +/- verbosity · / search\n"+
		"key format: "+consoleKeyFormat+"\n")
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

// consoleKeyNames mirrors the (unexported) named-key table tuist.ParseKey
// accepts, so /key can reject a token ParseKey would not recognize instead of
// letting it degrade into literal text.
var consoleKeyNames = map[string]bool{
	"enter": true, "tab": true, "backspace": true,
	"escape": true, "esc": true, "space": true,
	"up": true, "down": true, "left": true, "right": true,
	"home": true, "end": true, "pgup": true, "pgdown": true,
	"insert": true, "delete": true, "begin": true, "find": true, "select": true,
}

// consoleKeyMods mirrors tuist.ParseKey's modifier-prefix table.
var consoleKeyMods = map[string]bool{
	"ctrl": true, "alt": true, "shift": true,
	"meta": true, "super": true, "hyper": true,
}

// consoleFKey matches the function keys f1..f20, which tuist routes through
// its extended-key fallback: uv matches extended keys by their text, so they
// behave as real keys even though they're not in the named-key table.
var consoleFKey = regexp.MustCompile(`^f([1-9]|1[0-9]|20)$`)

// consoleKeyFormat describes the accepted /key token format, for error
// messages and /help.
const consoleKeyFormat = "named keys (enter, tab, backspace, esc/escape, space, " +
	"up, down, left, right, home, end, pgup, pgdown, insert, delete, begin, " +
	"find, select), f1-f20, any single character, or a '+'-joined modifier " +
	"combo of ctrl/alt/shift/meta/super/hyper (e.g. ctrl+s, alt+enter); " +
	"'<key>*N' repeats a token"

// validateConsoleKey reports whether tuist.ParseKey would treat spec as a real
// key. ParseKey itself never fails: an unknown multi-rune name falls back to
// an extended key carrying the name as literal text, which a focused text
// input happily inserts — so an unsupported token like "C-s" would be typed
// verbatim into the TUI (corrupting e.g. an agent prompt) rather than erroring.
func validateConsoleKey(spec string) error {
	parts := strings.Split(spec, "+")
	for i, part := range parts {
		switch {
		case part == "":
			// An empty part comes from a "+" in the spec ("+" splits to
			// ["",""], "ctrl++" to ["ctrl","",""]) — the plus key itself.
		case i < len(parts)-1 && consoleKeyMods[part]:
			// A modifier prefix ("ctrl+...", "alt+...").
		case consoleKeyNames[part],
			consoleFKey.MatchString(part),
			utf8.RuneCountInString(part) == 1:
			// A named key, an f-key, or a bare character.
		default:
			return fmt.Errorf("unknown key %q in token %q: want %s", part, spec, consoleKeyFormat)
		}
	}
	return nil
}

// consoleDuration parses a /wait duration param: a Go duration string ("2s",
// "1500ms") or a bare number of seconds ("30", "2.5"). Empty means def.
func consoleDuration(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		secs, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil {
			return 0, fmt.Errorf("bad duration %q: want a Go duration (e.g. \"2s\") or seconds (e.g. \"30\")", s)
		}
		d = time.Duration(secs * float64(time.Second))
	}
	if d < 0 {
		return 0, fmt.Errorf("bad duration %q: must not be negative", s)
	}
	return d, nil
}

// LLMToolsProvider renders the documentation of the tools currently exposed to
// an interactive LLM session's model (LLM.tools). The CLI registers one when
// an agent/shell session starts so the console can serve /toolset.
type LLMToolsProvider func(context.Context) (string, error)

// SetLLMToolsProvider registers the toolset provider backing the console's
// /toolset endpoint. The CLI re-registers it on every LLM swap (prompt turns,
// .clear, .model, resume, ...) so it always reflects the current composition.
func (fe *frontendPretty) SetLLMToolsProvider(fn LLMToolsProvider) {
	fe.dispatch(func() {
		fe.llmToolsFn = fn
	})
}

// Agent extraction is deliberately request-driven and console-only. Discovery
// uses the same roster as --trace restoration; transcripts come from committed
// checkpoints or runtime snapshots, never log buffers or rendered rows.
// No observers, buffers, or callbacks are installed on the production path.
type consoleAgent struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	State          string   `json:"state"`
	ParentID       string   `json:"parentID,omitempty"`
	SpanIDs        []string `json:"spanIDs"`
	SnapshotDigest string   `json:"snapshotDigest,omitempty"`
}

func (fe *frontendPretty) consoleAgents() []consoleAgent {
	return consoleAgentsFromDB(fe.db)
}

func consoleAgentsFromDB(db *dagui.DB) []consoleAgent {
	agents := make([]consoleAgent, 0)
	for _, node := range db.Agents() {
		agent := consoleAgent{
			ID: node.ID, Name: node.Name, State: node.State,
			SnapshotDigest: node.SnapshotDigest, SpanIDs: []string{},
		}
		for _, span := range node.Spans {
			agent.SpanIDs = append(agent.SpanIDs, span.ID.String())
			for parent := span.ParentSpan; parent != nil; parent = parent.ParentSpan {
				if parent.Agent && parent.AgentID != "" && parent.AgentID != node.ID {
					agent.ParentID = parent.AgentID
					break
				}
			}
		}
		agents = append(agents, agent)
	}
	return agents
}

func (fe *frontendPretty) consoleAgentsHandler(w http.ResponseWriter, _ *http.Request) {
	fe.consoleMu.Lock()
	if fe.tui != nil {
		fe.tui.Step()
	}
	agents := fe.consoleAgents()
	connected := fe.dag != nil
	fe.consoleMu.Unlock()
	consoleJSON(w, struct {
		Agents          []consoleAgent `json:"agents"`
		LoadedSpansOnly bool           `json:"loadedSpansOnly"`
		EngineConnected bool           `json:"engineConnected"`
		Note            string         `json:"note"`
	}{agents, true, connected, "Use dagger agent --trace to restore the full roster before reading transcripts. Discovery does not start agents."})
}

type consoleTranscriptBlock struct {
	Kind      string `json:"kind"`
	Text      string `json:"text,omitempty"`
	CallID    string `json:"callId,omitempty"`
	ToolName  string `json:"toolName,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Errored   bool   `json:"errored,omitempty"`
}

type consoleTranscriptOrigin struct {
	Kind      string `json:"kind"`
	AgentName string `json:"agentName,omitempty"`
	Ref       string `json:"ref,omitempty"`
	ReplyTo   string `json:"replyTo,omitempty"`
}

type consoleTranscriptMessage struct {
	Index   int                      `json:"index"`
	Role    string                   `json:"role"`
	Origin  *consoleTranscriptOrigin `json:"origin,omitempty"`
	Content []consoleTranscriptBlock `json:"content"`
}

type consoleAgentSnapshot struct {
	Source   string `json:"-"`
	Digest   string `json:"-"`
	State    string `json:"state"`
	Snapshot struct {
		ID       string                     `json:"id"`
		Messages []consoleTranscriptMessage `json:"messages"`
	} `json:"snapshot"`
}

type consoleSnapshotReader func(context.Context, string, string) (consoleAgentSnapshot, error)

// Only pure lookup and snapshot reads: in particular no spawn, send, resume,
// wait, tool listing, or bound receiver evaluation. Suppress observation traffic
// so repeatedly reading a long conversation doesn't grow its telemetry.
const consoleTranscriptQuery = `query ConsoleTranscript($handle: String!, $name: String!) {
  llm {
    agent(handle: $handle, name: $name) {
      state
      snapshot {
        id
        messages {
          role
          origin { kind agentName ref replyTo }
          content { kind text callId toolName arguments errored }
        }
      }
    }
  }
}`

func readConsoleAgentSnapshot(ctx context.Context, dag *dagger.Client, handle, name string) (consoleAgentSnapshot, error) {
	var res struct {
		LLM struct {
			Agent consoleAgentSnapshot `json:"agent"`
		} `json:"llm"`
	}
	err := dag.Do(engine.ContextWithTelemetrySuppression(ctx), &dagger.Request{
		Query: consoleTranscriptQuery, OpName: "ConsoleTranscript",
		Variables: map[string]any{"handle": handle, "name": name},
	}, &dagger.Response{Data: &res})
	res.LLM.Agent.Source = "runtime-snapshot"
	return res.LLM.Agent, err
}

func (fe *frontendPretty) consoleTranscriptHandler(w http.ResponseWriter, r *http.Request) {
	fe.consoleMu.Lock()
	dag := fe.dag
	fe.consoleMu.Unlock()
	var read consoleSnapshotReader
	if dag != nil {
		read = func(ctx context.Context, handle, name string) (consoleAgentSnapshot, error) {
			return readConsoleAgentSnapshot(ctx, dag, handle, name)
		}
	}
	fe.serveConsoleTranscript(w, r, read)
}

func (fe *frontendPretty) serveConsoleTranscript(w http.ResponseWriter, r *http.Request, read consoleSnapshotReader) {
	fe.consoleMu.Lock()
	if fe.tui != nil {
		fe.tui.Step()
	}
	agents := fe.consoleAgents()
	fe.consoleMu.Unlock()
	serveConsoleTranscript(w, r, agents, read)
}

func serveConsoleTranscript(w http.ResponseWriter, r *http.Request, agents []consoleAgent, read consoleSnapshotReader) {
	q := r.URL.Query()
	name := q.Get("agent")
	if name == "" {
		http.Error(w, "agent is required: use /agents, then select a handle or unique name", http.StatusBadRequest)
		return
	}
	role := strings.ToUpper(q.Get("role"))
	if role != "" && role != "USER" && role != "ASSISTANT" && role != "SYSTEM" {
		http.Error(w, "role must be user, assistant, or system", http.StatusBadRequest)
		return
	}
	offset, limit := 0, 20
	var err error
	if raw := q.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			http.Error(w, "offset must be a non-negative integer", http.StatusBadRequest)
			return
		}
	}
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			http.Error(w, "limit must be between 1 and 100", http.StatusBadRequest)
			return
		}
	}
	var grep *regexp.Regexp
	if pattern := q.Get("grep"); pattern != "" {
		grep, err = regexp.Compile(pattern)
		if err != nil {
			http.Error(w, "invalid grep regexp: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	var matches []consoleAgent
	for _, agent := range agents {
		if agent.ID == name {
			matches = []consoleAgent{agent}
			break
		}
		if agent.Name == name {
			matches = append(matches, agent)
		}
	}
	if len(matches) == 0 {
		http.Error(w, "agent not found in loaded roster; use /agents", http.StatusNotFound)
		return
	}
	if len(matches) > 1 {
		http.Error(w, "agent name is ambiguous; select a handle from /agents", http.StatusConflict)
		return
	}
	if read == nil {
		http.Error(w, "transcripts require an engine session: use dagger agent --trace <trace-id> without prompting the agents", http.StatusConflict)
		return
	}
	// The network read must not hold consoleMu, change focus, or drive a turn.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	snapshot, err := read(ctx, matches[0].ID, matches[0].Name)
	if err != nil {
		http.Error(w, "read agent checkpoint: "+err.Error(), http.StatusBadGateway)
		return
	}
	if snapshot.Snapshot.ID == "" && snapshot.Digest == "" {
		http.Error(w, "engine returned no committed snapshot", http.StatusBadGateway)
		return
	}
	messages := selectConsoleTranscript(snapshot.Snapshot.Messages, role, q.Get("tool"), grep)
	total := len(messages)
	start := min(offset, total)
	end := start + min(limit, total-start)
	agent := matches[0]
	agent.State = snapshot.State
	consoleJSON(w, struct {
		Agent          consoleAgent               `json:"agent"`
		Source         string                     `json:"source"`
		SnapshotDigest string                     `json:"snapshotDigest,omitempty"`
		SnapshotID     string                     `json:"snapshotID,omitempty"`
		Total          int                        `json:"total"`
		Offset         int                        `json:"offset"`
		Limit          int                        `json:"limit"`
		HasMore        bool                       `json:"hasMore"`
		Messages       []consoleTranscriptMessage `json:"messages"`
	}{agent, snapshot.Source, snapshot.Digest, snapshot.Snapshot.ID, total, offset, limit, end < total, messages[start:end]})
}

func selectConsoleTranscript(messages []consoleTranscriptMessage, role, tool string, grep *regexp.Regexp) []consoleTranscriptMessage {
	selected := make([]consoleTranscriptMessage, 0)
	for index, message := range messages {
		if role == "" && message.Role == "SYSTEM" || role != "" && message.Role != role {
			continue
		}
		toolMatches := tool == ""
		var text strings.Builder
		for _, block := range message.Content {
			toolMatches = toolMatches || block.Kind == "TOOL_CALL" && block.ToolName == tool
			if grep != nil {
				text.WriteString(block.Text)
				text.WriteString("\n")
				text.WriteString(block.ToolName)
				text.WriteString("\n")
				text.WriteString(block.Arguments)
				text.WriteString("\n")
			}
		}
		if !toolMatches || grep != nil && !grep.MatchString(text.String()) {
			continue
		}
		message.Index = index
		selected = append(selected, message)
	}
	return selected
}

func consoleJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	// Preserve HTML-like source text verbatim; JSON encoding still escapes
	// control characters without terminal wrapping or ANSI interpretation.
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
}
