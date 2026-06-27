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

// traceSession drives the real interactive pretty frontend as a black box. It
// builds the frontend on a headless terminal, wires fixture-backed span/log
// providers through the SAME public seams the CLI uses (SetSpanProvider /
// SetLogProvider / ImportSnapshots / LogExporter), and advances the UI with
// tui.Step — the synchronous equivalent of the event loop. A test reads like:
//
//	sess := newTraceSession(t, fix, nil)
//	sess.Render()             // wait for output
//	sess.Press("down", "+")   // send keystrokes (real focus routing)
//	sess.Render()             // expect output
//	sess.Network()            // observe fetches (the sanctioned side-channel)
//
// The only "test side channel" is the provider closures: like the CLI's Cloud
// client they serve the fixture and count what's fetched, so the frontend's
// network behaviour is observable without reaching into its internals.
//
// Fetches are modelled deterministically: a provider call during a key press or
// render only RECORDS the request and QUEUES the data (it never mutates the
// frontend mid-render). settle() then delivers queued arrivals through the real
// public exporters and Steps again, so the next frame reflects them — the same
// re-render-on-arrival an interactive session resolves over successive frames.
type traceSession struct {
	t    *testing.T
	fe   *frontendPretty
	term *tuist.HeadlessTerminal
	fix  *TraceFixture
	net  *fetchStats

	childOf       map[string][]dagui.SpanSnapshot // parent hex -> children
	listened      map[string]bool                 // spans whose children were served
	requestedLogs map[string]bool                 // (hex|desc) already served

	pendingSpans []dagui.SpanSnapshot
	pendingLogs  []cloud.LogMessage
}

// newTraceSession builds the frontend, wires the fixture-backed providers, loads
// the priority spans, and settles the first frame. configure (may be nil) tweaks
// FrontendOpts before the first render (e.g. verbosity, window size).
func newTraceSession(t *testing.T, fix *TraceFixture, configure func(*dagui.FrontendOpts)) *traceSession {
	t.Helper()
	t.Setenv("NO_COLOR", "1")

	s := &traceSession{
		t:             t,
		fix:           fix,
		net:           newFetchStats(),
		term:          tuist.NewHeadlessTerminal(120, 40),
		childOf:       map[string][]dagui.SpanSnapshot{},
		listened:      map[string]bool{},
		requestedLogs: map[string]bool{},
	}
	for _, sp := range fix.Spans {
		if sp.ParentID.IsValid() {
			ph := sp.ParentID.String()
			s.childOf[ph] = append(s.childOf[ph], sp)
		}
	}

	s.fe = newWithTerminal(io.Discard, dagui.NewDB(), s.term)

	// Apply the opts the CLI would pass to Run, including Run's defaults, then
	// let the test override. We don't call Run (it blocks on the event loop);
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
	s.fe.SetTraceID(fix.TraceID)
	s.fe.SetSpanProvider(s.serveSpans)
	s.fe.SetLogProvider(s.serveLogs)

	// loadInitial: the priority spans arrive as the root load.
	prio := fix.prioritySet()
	var initial []dagui.SpanSnapshot
	for _, sp := range fix.Spans {
		if prio[sp.ID.String()] {
			initial = append(initial, sp)
		}
	}
	s.fe.ImportSnapshots(initial)

	s.settle()
	return s
}

// serveSpans is the fixture-backed span provider (lazy child load on expand).
func (s *traceSession) serveSpans(id dagui.SpanID) {
	hex := id.String()
	if s.listened[hex] {
		return
	}
	s.listened[hex] = true
	kids := s.childOf[hex]
	if len(kids) == 0 {
		return
	}
	s.net.add(opSpanUpdates, len(kids), jsonBytes(kids))
	s.pendingSpans = append(s.pendingSpans, kids...)
}

// serveLogs is the fixture-backed log provider. descendants picks the rolled-up
// variant and re-keys the logs onto the fetched span, exactly like the CLI's
// fetchSpanLogs (internal/cmd/dagger/trace.go).
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

	fl := s.fix.Logs[hex]
	msgs := fl.Own
	if descendants {
		msgs = fl.Roll
		// Attribute rolled-up descendants to the span we fetched for, matching
		// fetchSpanLogs, so a check/test shows its sub-operation's output.
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
		records := cloud.LogMessagesToRecords(s.fix.TraceID, s.pendingLogs)
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

// Resize changes the terminal dimensions (e.g. to test reflow) and settles.
func (s *traceSession) Resize(width, height int) string {
	s.term.Resize(width, height)
	return s.settle()
}

// Zoom scopes the view to a span (the --span deep link), fetching its subtree
// on demand, then settles. Handy for jumping straight to a misbehaving span.
func (s *traceSession) Zoom(id dagui.SpanID) string {
	s.fe.ZoomToSpan(id)
	return s.settle()
}

// Network returns the fetch counters accumulated so far — the sanctioned
// side-channel for asserting on network behaviour (what the CLI's --debug
// prints).
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
