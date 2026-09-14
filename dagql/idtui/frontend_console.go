package idtui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
		io.WriteString(w, fe.consoleSpans(r.URL.Query().Get("q")))
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
		detail, ok := fe.consoleSpanDetail(dagui.SpanID{SpanID: sid})
		if !ok {
			http.Error(w, fmt.Sprintf("unknown span %s", hex), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, detail)
	})
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

// consoleSpans lists the spans currently loaded in the DB (everything fetched so
// far), optionally filtered by a name substring, so a caller can find a span hex
// to zoom to. Service-instance spans are tagged with their hostname so running
// services (whose logs live beneath them) are cheap to find.
func (fe *frontendPretty) consoleSpans(q string) string {
	var b strings.Builder
	for _, sp := range fe.db.Spans.Order {
		if q != "" && !strings.Contains(sp.Name, q) && !strings.Contains(sp.ServiceName, q) {
			continue
		}
		name := sp.Name
		if sp.Service {
			tag := "service"
			if sp.ServiceName != "" {
				tag += " " + sp.ServiceName
			}
			name += "  [" + tag + "]"
		}
		fmt.Fprintf(&b, "%s  %-5s  %s\n", sp.ID, consoleSpanStatus(sp), name)
	}
	return b.String()
}

// consoleSpanStatus classifies a span with the same vocabulary /spans uses.
// A span often passes its own OTel status through while the failure rides on a
// link (a test/check whose error is on a descendant or linked span). Surface
// that as FAIL so a caller can still find it by name, distinct from ERROR (the
// span itself errored).
func consoleSpanStatus(sp *dagui.Span) string {
	switch {
	case sp.IsFailed():
		return "ERROR"
	case sp.IsFailedOrCausedFailure():
		return "FAIL"
	case sp.IsRunning():
		return "run"
	default:
		return "ok"
	}
}

// consoleSpanFlags lists the dagui flags set on a span that shape how the UI
// treats it (visibility, encapsulation, log/span roll-up) — only the ones that
// are actually set, so ancestor-chain lines stay compact.
func consoleSpanFlags(sp *dagui.Span) []string {
	var flags []string
	set := func(on bool, name string) {
		if on {
			flags = append(flags, name)
		}
	}
	set(sp.Internal, "internal")
	set(sp.Boundary, "boundary")
	set(sp.Encapsulate, "encapsulate")
	set(sp.Encapsulated, "encapsulated")
	set(sp.Passthrough, "passthrough")
	set(sp.Ignore, "ignore")
	set(sp.Reveal, "reveal")
	set(sp.RollUpLogs, "rollUpLogs")
	set(sp.RollUpSpans, "rollUpSpans")
	set(sp.Cached, "cached")
	set(sp.Canceled, "canceled")
	switch {
	case sp.Service && sp.ServiceName != "":
		flags = append(flags, "service="+sp.ServiceName)
	case sp.Service:
		flags = append(flags, "service")
	case sp.ServiceName != "":
		flags = append(flags, "serviceName="+sp.ServiceName)
	}
	if sp.LLMRole != "" {
		flags = append(flags, "llmRole="+sp.LLMRole)
	}
	if sp.LLMTool != "" {
		flags = append(flags, "llmTool="+sp.LLMTool)
	}
	return flags
}

// consoleSpanDetail reports one span in depth: status, timing, the UI-shaping
// flags, and the parent chain up to the root — each ancestor with its own set
// flags, which is what debugging "why is this span hidden / why didn't its
// logs roll up" needs. Returns false if the span isn't loaded in the DB.
func (fe *frontendPretty) consoleSpanDetail(id dagui.SpanID) (string, bool) {
	sp, ok := fe.db.Spans.Map[id]
	if !ok || sp == nil {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "span:     %s  %s\n", sp.ID, sp.Name)
	fmt.Fprintf(&b, "status:   %s\n", consoleSpanStatus(sp))
	if sp.StartTime.IsZero() {
		fmt.Fprintf(&b, "started:  (unknown)\n")
	} else {
		fmt.Fprintf(&b, "started:  %s\n", sp.StartTime.Format(time.RFC3339Nano))
		if sp.IsRunning() {
			// Running spans have no end time yet (dagui encodes that as
			// EndTime < StartTime); show elapsed time instead.
			fmt.Fprintf(&b, "ended:    (still running)\n")
			fmt.Fprintf(&b, "duration: %s (so far)\n", time.Since(sp.StartTime).Truncate(time.Millisecond))
		} else {
			fmt.Fprintf(&b, "ended:    %s\n", sp.EndTime.Format(time.RFC3339Nano))
			fmt.Fprintf(&b, "duration: %s\n", sp.EndTime.Sub(sp.StartTime).Truncate(time.Millisecond))
		}
	}
	if flags := consoleSpanFlags(sp); len(flags) > 0 {
		fmt.Fprintf(&b, "flags:    %s\n", strings.Join(flags, " "))
	} else {
		fmt.Fprintf(&b, "flags:    (none)\n")
	}
	fmt.Fprintf(&b, "parents (nearest first):\n")
	if sp.ParentSpan == nil {
		fmt.Fprintf(&b, "  (none — root span)\n")
	}
	for parent := sp.ParentSpan; parent != nil; parent = parent.ParentSpan {
		line := fmt.Sprintf("  %s  %s", parent.ID, parent.Name)
		if flags := consoleSpanFlags(parent); len(flags) > 0 {
			line += "  [" + strings.Join(flags, " ") + "]"
		}
		fmt.Fprintf(&b, "%s\n", line)
	}
	return b.String(), true
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
	detail, ok := fe.consoleTimings(dagui.SpanID{SpanID: sid}, minDuration, limit, time.Now())
	if !ok {
		http.Error(w, fmt.Sprintf("span %s is not loaded", hex), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, detail)
}

// consoleTimings reports raw parent/child timing, not the UI's filtered or
// linked tree. It never fetches telemetry or logs. now is shared by all running
// rows so their elapsed durations describe one snapshot.
func (fe *frontendPretty) consoleTimings(id dagui.SpanID, minDuration time.Duration, limit int, now time.Time) (string, bool) {
	root, ok := fe.db.Spans.Map[id]
	if !ok || root == nil || !root.Received {
		return "", false
	}
	children := make(map[dagui.SpanID][]*dagui.Span)
	for _, sp := range fe.db.Spans.Order {
		if sp.Received {
			children[sp.ParentID] = append(children[sp.ParentID], sp)
		}
	}
	var spans []*dagui.Span
	seen := make(map[dagui.SpanID]bool)
	pending := []*dagui.Span{root}
	for len(pending) > 0 {
		sp := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[sp.ID] {
			continue
		}
		seen[sp.ID] = true
		spans = append(spans, sp)
		pending = append(pending, children[sp.ID]...)
	}
	slices.SortFunc(spans, func(a, b *dagui.Span) int {
		// Unknown starts sort last; equal starts have a stable ID tie-break.
		if a.StartTime.IsZero() != b.StartTime.IsZero() {
			if a.StartTime.IsZero() {
				return 1
			}
			return -1
		}
		if cmp := a.StartTime.Compare(b.StartTime); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	var b strings.Builder
	fmt.Fprintf(&b, "root: %s  %q\n", root.ID, root.Name)
	fmt.Fprintln(&b, "Loaded spans only (including internal); incomplete if telemetry is missing or not fetched. No logs fetched.")
	fmt.Fprintln(&b, "Durations are wall time, may overlap, and are not CPU self time; 'so far' uses the current clock. Unknown timings survive the duration filter.")
	fmt.Fprintln(&b, "span_id  parent_id  start_offset  duration  name")
	shown, filtered, capped := 0, 0, 0
	for _, sp := range spans {
		offset, duration := "unknown", "unknown"
		if !sp.StartTime.IsZero() {
			elapsed := sp.EndTime.Sub(sp.StartTime)
			if sp.IsRunning() {
				elapsed = now.Sub(sp.StartTime)
			}
			if elapsed < minDuration {
				filtered++
				continue
			}
			duration = elapsed.String()
			if sp.IsRunning() {
				duration += " (so far)"
			}
			if !root.StartTime.IsZero() {
				offset = sp.StartTime.Sub(root.StartTime).String()
			}
		}
		if limit > 0 && shown >= limit {
			capped++
			continue
		}
		fmt.Fprintf(&b, "%s  %s  %s  %s  %q\n", sp.ID, sp.ParentID, offset, duration, sp.Name)
		shown++
	}
	fmt.Fprintf(&b, "shown: %d; omitted: %d (%d below minDuration, %d over limit); loaded subtree: %d\n", shown, filtered+capped, filtered, capped, len(spans))
	return b.String(), true
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
		"  GET  /span?id=<hex>  span detail: status, timing, flags, parent chain\n"+
		"  GET  /timings?root=<hex>[&minDuration=10ms&limit=200]  loaded subtree wall timings (0 limit = unlimited)\n"+
		"  GET  /toolset        the interactive LLM session's tool docs, when one is live\n"+
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
