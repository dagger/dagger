package server

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// wcprofSessionCompleteSpanName is the name of the session-teardown count carrier
// span (see wcprofSpanCounter). OnStart matches it to exclude the carrier from the
// total it declares.
const wcprofSessionCompleteSpanName = "wcprof.session_complete"

// wcprofSpanCounter is the producer half of the wcprof completeness checksum
// (leaf-drop detection). A span the engine drops on the way to Cloud
// is undetectable from the trace if it is a LEAF (it breaks no parent/wait edge),
// so the reference-based structural gate cannot catch it; the engine therefore
// DECLARES how many spans it emitted and the loader refuses a trace that received
// fewer.
//
// It is an sdktrace.SpanProcessor registered on EVERY per-client tracer provider
// (main and nested module-runtime clients), all sharing this one instance, so the
// per-trace count accumulates across the whole nested-client tree of a session. At
// span creation OnStart (a) marks the span WcprofEngineSpanAttr — the counted
// population, which lets the loader exclude CLI-shell and otelhttp/buildkit spans
// the engine never counts — and (b) increments the trace's count.
//
// The total is declared ONCE, at session teardown: removeDaggerSession drains the
// session's queries and stops its services (so no more engine spans will be
// created), reads the EXACT Final count, and stamps it on a dedicated carrier span
// (wcprofSessionCompleteSpanName). Because that declaration is the exact final —
// not a per-query running floor — received <= declared ALWAYS holds, so ANY drop
// (an individual leaf, a whole trailing query whose own root is lost, or
// post-query async padding) shows up as received < declared and is caught. Reap
// then drops the per-trace entry, bounding the map to live traces.
//
// The carrier must not be counted in the total it carries: OnStart skips the span
// named wcprofSessionCompleteSpanName (no mark, no increment), so it is excluded
// from both the declared count and the loader's received tally.
//
// Scope (the engine-vs-CLI population decision, design point 4): the count covers
// ENGINE spans only (the ranking-critical class: call/call_exec/exec/lazy/service +
// user-work + module-load). CLI-shell spans are out of scope and unmarked, so they
// never skew the reconciliation. The only residual is an engine span created AFTER
// the teardown stamp (e.g. a container-release span on a non-per-client tracer);
// such spans are not on a counted tracer and/or are past telemetry shutdown, so
// they neither inflate received nor escape the count — verified empirically that a
// complete capture reconciles received == declared exactly.
type wcprofSpanCounter struct {
	mu     sync.Mutex
	counts map[trace.TraceID]int
}

func newWcprofSpanCounter() *wcprofSpanCounter {
	return &wcprofSpanCounter{counts: map[trace.TraceID]int{}}
}

func (c *wcprofSpanCounter) OnStart(_ context.Context, s sdktrace.ReadWriteSpan) {
	tid := s.SpanContext().TraceID()
	if !tid.IsValid() {
		return
	}
	// The teardown carrier rides in only to DECLARE the total; it must not be part
	// of it. Skip it (no mark, no count) so it is excluded from declared and received.
	if s.Name() == wcprofSessionCompleteSpanName {
		return
	}
	// Mark the engine population (one cheap bool attr); the loader counts these.
	s.SetAttributes(attribute.Bool(telemetryattrs.WcprofEngineSpanAttr, true))
	c.mu.Lock()
	c.counts[tid]++
	c.mu.Unlock()
}

func (c *wcprofSpanCounter) OnEnd(sdktrace.ReadOnlySpan)      {}
func (c *wcprofSpanCounter) Shutdown(context.Context) error   { return nil }
func (c *wcprofSpanCounter) ForceFlush(context.Context) error { return nil }

// Final returns the current engine span total for tid WITHOUT reaping it. The
// session-teardown handler calls it once span emission for the trace has quiesced
// (queries drained, services stopped), so the value is the EXACT final total. It is
// then stamped on the carrier span and read back by the loader as the authoritative
// declared count.
func (c *wcprofSpanCounter) Final(tid trace.TraceID) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[tid]
}

// Reap drops the per-trace counter entry. The session-teardown handler calls it
// after stamping the final count, once the session (hence the trace) is done,
// keeping the map bounded to live traces.
func (c *wcprofSpanCounter) Reap(tid trace.TraceID) {
	if !tid.IsValid() {
		return
	}
	c.mu.Lock()
	delete(c.counts, tid)
	c.mu.Unlock()
}
