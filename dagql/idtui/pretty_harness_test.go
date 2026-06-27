package idtui

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/cloud"
)

// prettyHarness drives the interactive (non-report) pretty frontend from a test:
// it loads a real (or hand-built) TraceFixture, wires fake span/log providers
// that serve the fixture and COUNT what's fetched (like the cloud client's
// --debug stats), and exposes expand/zoom/key controls + Render(). No pty, no
// CLI, no Cloud connection.
//
// Data flow mirrors the real loader but is deterministic: a provider call during
// expand or render only RECORDS the request and queues the data (it never
// mutates the frontend mid-render); deliver() then applies the arrivals
// synchronously, and the next render reflects them -- modelling the real
// re-render-on-arrival without an event loop. settle() runs that loop to quiet,
// draining lazy fetches the way an interactive session would.
type prettyHarness struct {
	t   *testing.T
	fe  *frontendPretty
	fix *TraceFixture

	stats         *fetchStats
	childOf       map[string][]dagui.SpanSnapshot // parent hex -> children
	listened      map[string]bool                 // spans whose children were served
	requestedLogs map[string]bool                 // (hex|desc) already served

	pendingSpans []dagui.SpanSnapshot
	pendingLogs  []cloud.LogMessage
}

// newPrettyHarness builds the frontend, wires the fixture-backed providers, and
// loads the priority spans. configure (may be nil) tweaks FrontendOpts before
// the first render (e.g. verbosity, window size).
func newPrettyHarness(t *testing.T, fix *TraceFixture, configure func(*frontendPretty)) *prettyHarness {
	t.Helper()
	t.Setenv("NO_COLOR", "1")

	h := &prettyHarness{
		t:             t,
		fix:           fix,
		stats:         newFetchStats(),
		childOf:       map[string][]dagui.SpanSnapshot{},
		listened:      map[string]bool{},
		requestedLogs: map[string]bool{},
	}
	for _, s := range fix.Spans {
		if s.ParentID.IsValid() {
			ph := s.ParentID.String()
			h.childOf[ph] = append(h.childOf[ph], s)
		}
	}

	h.fe = NewWithDB(io.Discard, dagui.NewDB())
	h.fe.traceID = fix.TraceID
	// A terminal-sized window so the tree renders with real layout (otherwise
	// the zero window collapses most output).
	h.fe.setWindowSizeLocked(windowSize{Width: 120, Height: 40})
	h.fe.autoFocus = true
	if configure != nil {
		configure(h.fe)
	}
	// Set providers directly: SetSpanProvider/SetLogProvider dispatch onto the
	// (absent) event loop, so the assignment would never take effect in a test.
	h.fe.logProvider = h.serveLogs
	h.fe.spanProvider = h.serveSpans

	// loadInitial: the priority spans arrive as the root load.
	prio := fix.prioritySet()
	var initial []dagui.SpanSnapshot
	for _, s := range fix.Spans {
		if prio[s.ID.String()] {
			initial = append(initial, s)
		}
	}
	h.fe.db.ImportSnapshots(initial)
	h.fe.viewDirty = true
	h.render() // first render, drains any eager (render-driven) requests
	h.settle()
	return h
}

// serveSpans is the fixture-backed span provider (lazy child load on expand).
func (h *prettyHarness) serveSpans(id dagui.SpanID) {
	hex := id.String()
	if h.listened[hex] {
		return
	}
	h.listened[hex] = true
	kids := h.childOf[hex]
	if len(kids) == 0 {
		return
	}
	h.stats.add(opSpanUpdates, len(kids), jsonBytes(kids))
	h.pendingSpans = append(h.pendingSpans, kids...)
}

// serveLogs is the fixture-backed log provider. descendants picks the rolled-up
// variant and re-keys the logs onto the fetched span, exactly like the CLI's
// fetchSpanLogs (internal/cmd/dagger/trace.go).
func (h *prettyHarness) serveLogs(id dagui.SpanID, descendants bool) {
	hex := id.String()
	key := hex
	if descendants {
		key += "|d"
	}
	if h.requestedLogs[key] {
		return
	}
	h.requestedLogs[key] = true

	fl := h.fix.Logs[hex]
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
	h.stats.logRequests = append(h.stats.logRequests, hex)
	h.stats.add(opSpanLogs, len(msgs), jsonBytes(msgs))
	h.pendingLogs = append(h.pendingLogs, msgs...)
}

// deliver applies queued span/log arrivals to the frontend synchronously
// (bypassing the event-loop dispatch). Returns whether anything was delivered.
func (h *prettyHarness) deliver() bool {
	if len(h.pendingSpans) == 0 && len(h.pendingLogs) == 0 {
		return false
	}
	if len(h.pendingSpans) > 0 {
		h.fe.db.ImportSnapshots(h.pendingSpans)
		h.pendingSpans = nil
	}
	if len(h.pendingLogs) > 0 {
		records := cloud.LogMessagesToRecords(h.fix.TraceID, h.pendingLogs)
		ctx := context.Background()
		_ = h.fe.db.LogExporter().Export(ctx, records)
		_ = h.fe.logs.Export(ctx, records)
		h.pendingLogs = nil
	}
	h.fe.viewDirty = true
	return true
}

// render rebuilds the view if dirty and returns the rendered frame. Rendering
// fires the interactive render-site log requests (requestLogsOnRender,
// LogsView.OnMount), which queue fetches for the next deliver().
func (h *prettyHarness) render() string {
	if h.fe.viewDirty {
		h.fe.recalculateViewLocked()
	}
	return strings.Join(h.fe.tui.RenderLines(), "\n")
}

// settle drives render → deliver until no new data arrives, draining the lazy
// fetches an interactive session resolves over successive frames.
func (h *prettyHarness) settle() string {
	var out string
	for i := 0; i < 32; i++ {
		out = h.render()
		if !h.deliver() {
			return out
		}
	}
	h.t.Fatal("prettyHarness.settle did not converge after 32 frames")
	return out
}

// Render returns the current frame, settling lazy fetches first.
func (h *prettyHarness) Render() string { return h.settle() }

// Stats returns the fetch counters accumulated so far.
func (h *prettyHarness) Stats() *fetchStats { return h.stats }

// Expand toggles a span open via the real interactive path (setExpanded), which
// lazily requests its children and logs, then settles.
func (h *prettyHarness) Expand(id dagui.SpanID) string {
	h.fe.setExpanded(id, true)
	h.fe.syncAfterExpandToggle(id)
	return h.settle()
}

// Collapse toggles a span closed.
func (h *prettyHarness) Collapse(id dagui.SpanID) string {
	h.fe.setExpanded(id, false)
	h.fe.syncAfterExpandToggle(id)
	return h.settle()
}

// Zoom scopes the view to a span (mirrors --span / a deep link), which fetches
// its subtree.
func (h *prettyHarness) Zoom(id dagui.SpanID) string {
	h.fe.ZoomToSpan(id)
	return h.settle()
}

// Focus moves durable focus to a span (so a subsequent key acts on it).
func (h *prettyHarness) Focus(id dagui.SpanID) {
	h.fe.autoFocus = false
	h.fe.FocusedSpan = id
	h.fe.viewDirty = true
}

// PressKey feeds a key through the real input handler, then settles.
func (h *prettyHarness) PressKey(k uv.Key) string {
	h.fe.handleNavKeyUV(uv.KeyPressEvent(k))
	return h.settle()
}

// jsonBytes approximates the wire size of a fetched batch (the real client
// counts raw SSE payload bytes; JSON size is the closest deterministic proxy).
func jsonBytes(v any) int64 {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return int64(len(b))
}
