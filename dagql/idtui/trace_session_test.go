package idtui

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/vito/tuist"
)

// traceSource supplies a trace's spans and logs to a traceSession on demand,
// the way the CLI fetches from Cloud: the priority spans up front, then a span's
// children and logs lazily when it's expanded or rendered. liveSource hits
// Cloud (trace_live_test.go); staticSource serves an in-memory tree for offline
// smoke tests (trace_static_test.go).
type traceSource interface {
	// traceID is the hex trace id (for log record attribution).
	traceID() string
	// loadInitial returns the priority (root) spans.
	loadInitial() []dagui.SpanSnapshot
	// children returns a span's children, fetched on demand; nil when it has
	// none or the whole tree is already loaded (expanding is then local).
	children(id dagui.SpanID) []dagui.SpanSnapshot
	// logs returns a span's logs; descendants selects the rolled-up form.
	logs(id dagui.SpanID, descendants bool) []cloud.LogMessage
}

// traceSession drives the real interactive pretty frontend as a black box,
// headless — no pty, no CLI, no event-loop goroutine. It builds the frontend on
// a headless terminal, wires span/log providers through the SAME public seams
// the CLI uses (SetSpanProvider/SetLogProvider/ImportSnapshots/LogExporter), and
// advances the UI with tui.Step. It's the closest thing to the real TUI that can
// be scripted or curled:
//
//	sess := newTraceSession(t, src, nil)
//	sess.Render()               // current screen
//	sess.Press("down", "right") // keystrokes (real focus routing + lazy fetch)
//	sess.Network()              // what got fetched (the --debug side-channel)
//
// Fetches stay deterministic for the headless Step model: a provider records the
// request and queues the data (the fetch itself may block on Cloud, but it never
// mutates the frontend mid-render); settle() then delivers queued arrivals
// through the public exporters and Steps again, reproducing the real
// re-render-on-arrival across successive frames.
type traceSession struct {
	t    *testing.T
	fe   *frontendPretty
	term *tuist.HeadlessTerminal
	src  traceSource
	net  *fetchStats

	listened      map[string]bool // spans whose children were requested
	requestedLogs map[string]bool // (hex|desc) already requested

	pendingSpans []dagui.SpanSnapshot
	pendingLogs  []cloud.LogMessage
}

// newTraceSession builds the frontend, wires the providers, loads the priority
// spans, and settles the first frame. configure (may be nil) tweaks FrontendOpts
// before the first render (e.g. verbosity).
func newTraceSession(t *testing.T, src traceSource, configure func(*dagui.FrontendOpts)) *traceSession {
	t.Helper()
	t.Setenv("NO_COLOR", "1")

	s := &traceSession{
		t:             t,
		src:           src,
		net:           newFetchStats(),
		term:          tuist.NewHeadlessTerminal(120, 40),
		listened:      map[string]bool{},
		requestedLogs: map[string]bool{},
	}

	s.fe = newWithTerminal(io.Discard, dagui.NewDB(), s.term)

	// Apply the opts the CLI would pass to Run, including Run's defaults, then
	// let the caller override. We don't call Run (it blocks on the event loop);
	// this is the same configuration input by a different door.
	s.fe.FrontendOpts = dagui.FrontendOpts{
		Verbosity:        dagui.ShowCompletedVerbosity,
		TooFastThreshold: 100 * time.Millisecond,
		GCThreshold:      time.Hour,
	}
	if configure != nil {
		configure(&s.fe.FrontendOpts)
	}

	// Bring up the TUI without the event loop, then drive it via Step.
	s.fe.setupTUI()

	// Wire the real public seams, exactly like internal/cmd/dagger/trace.go.
	s.fe.SetTraceID(src.traceID())
	s.fe.SetSpanProvider(s.serveSpans)
	s.fe.SetLogProvider(s.serveLogs)

	// loadInitial: the priority spans arrive as the root load. We deliberately
	// do NOT SetPrimary: on a lazy trace only the root is loaded here, and
	// promoting its (unloaded) children to top-level would leave nothing to
	// render or focus until something is expanded.
	s.fe.ImportSnapshots(s.src.loadInitial())

	s.settle()
	return s
}

// serveSpans is the span provider: lazily fetch a span's children on expand.
func (s *traceSession) serveSpans(id dagui.SpanID) {
	hex := id.String()
	if s.listened[hex] {
		return
	}
	s.listened[hex] = true
	kids := s.src.children(id)
	if len(kids) == 0 {
		return
	}
	s.net.add(opSpanUpdates, len(kids), jsonBytes(kids))
	s.pendingSpans = append(s.pendingSpans, kids...)
}

// serveLogs is the log provider. descendants picks the rolled-up variant and
// re-keys the logs onto the fetched span, like the CLI's fetchSpanLogs, so a
// check/test shows its sub-operation's output.
func (s *traceSession) serveLogs(id dagui.SpanID, descendants bool) {
	hex := id.String()
	key := hex
	if descendants {
		key += "|d"
	}
	if s.requestedLogs[key] {
		return
	}
	s.requestedLogs[key] = true

	msgs := s.src.logs(id, descendants)
	if descendants {
		rekeyed := make([]cloud.LogMessage, len(msgs))
		for i, m := range msgs {
			m.SpanID = &hex
			rekeyed[i] = m
		}
		msgs = rekeyed
	}
	s.net.logRequests = append(s.net.logRequests, hex)
	s.net.add(opSpanLogs, len(msgs), jsonBytes(msgs))
	s.pendingLogs = append(s.pendingLogs, msgs...)
}

// deliver applies queued span/log arrivals through the real public exporters
// (the same path the CLI's loader/log streamer uses). Returns whether anything
// was delivered.
func (s *traceSession) deliver() bool {
	if len(s.pendingSpans) == 0 && len(s.pendingLogs) == 0 {
		return false
	}
	if len(s.pendingSpans) > 0 {
		s.fe.ImportSnapshots(s.pendingSpans)
		s.pendingSpans = nil
	}
	if len(s.pendingLogs) > 0 {
		records := cloud.LogMessagesToRecords(s.src.traceID(), s.pendingLogs)
		_ = s.fe.LogExporter().Export(context.Background(), records)
		s.pendingLogs = nil
	}
	return true
}

// settle Steps the TUI, delivering queued fetch arrivals between frames, until
// no new data arrives — draining the lazy fetches an interactive session would
// resolve over successive frames. Returns the final frame.
func (s *traceSession) settle() string {
	var out string
	for i := 0; i < 64; i++ {
		out = strings.Join(s.fe.tui.Step(), "\n")
		if !s.deliver() {
			return out
		}
	}
	s.t.Fatal("traceSession.settle did not converge after 64 frames")
	return out
}

// Render returns the current frame, settling lazy fetches first.
func (s *traceSession) Render() string { return s.settle() }

// Press feeds key presses through the real input path (tui.Inject ->
// dispatchEvent -> focus routing), then settles. Key names follow tuist.ParseKey
// (the frontend's keymap): "down"/"up"/"left"/"right", "enter", "esc", "space",
// "ctrl+c", and any printable rune ("+", "-", "/", ...).
func (s *traceSession) Press(keys ...string) string {
	for _, k := range keys {
		s.fe.tui.Inject(tuist.ParseKey(k))
	}
	return s.settle()
}

// Resize changes the terminal dimensions and settles.
func (s *traceSession) Resize(width, height int) string {
	s.term.Resize(width, height)
	return s.settle()
}

// Zoom scopes the view to a span (the --span deep link), fetching its subtree on
// demand, then settles. Handy for jumping straight to a misbehaving span.
func (s *traceSession) Zoom(id dagui.SpanID) string {
	s.fe.ZoomToSpan(id)
	return s.settle()
}

// Network returns the fetch counters accumulated so far — the side-channel for
// observing network behaviour (what the CLI's --debug prints).
func (s *traceSession) Network() *fetchStats { return s.net }

// jsonBytes approximates the wire size of a fetched batch (the real client
// counts raw SSE payload bytes; JSON size is the closest deterministic proxy).
func jsonBytes(v any) int64 {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return int64(len(b))
}

// fetchOp names a recorded fetch, matching the cloud client's --debug buckets.
type fetchOp string

const (
	opSpanUpdates fetchOp = "GetSpanUpdates"
	opSpanLogs    fetchOp = "GetSpanLogs"
)

// fetchStats accumulates per-op request/record/byte counts like the cloud
// client's clientStats (internal/cloud/stats.go), so a session can report
// "expanding span X fetched N log requests / K bytes" — the same numbers
// `dagger trace --debug` prints.
type fetchStats struct {
	ops map[fetchOp]*opCount
	// logRequests records the hex IDs whose logs were fetched, in order, so a
	// caller can assert exactly which spans were (and weren't) fetched.
	logRequests []string
}

type opCount struct {
	Requests int
	Records  int
	Bytes    int64
}

func newFetchStats() *fetchStats { return &fetchStats{ops: map[fetchOp]*opCount{}} }

func (s *fetchStats) add(op fetchOp, records int, bytes int64) {
	c := s.ops[op]
	if c == nil {
		c = &opCount{}
		s.ops[op] = c
	}
	c.Requests++
	c.Records += records
	c.Bytes += bytes
}

func (s *fetchStats) op(op fetchOp) opCount {
	if c := s.ops[op]; c != nil {
		return *c
	}
	return opCount{}
}

// fetchedLog reports whether a span's logs were ever requested.
func (s *fetchStats) fetchedLog(id dagui.SpanID) bool {
	hex := id.String()
	for _, h := range s.logRequests {
		if h == hex {
			return true
		}
	}
	return false
}
