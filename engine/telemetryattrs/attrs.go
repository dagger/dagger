package telemetryattrs

const (
	UIResumeOutputAttr = "dagger.io/ui.resume.output"

	// GenerateSkippedAttr marks a span reporting a workspace module that
	// best-effort `dagger generate` skipped because it could not be loaded. The
	// TUI collects these into a persisted "SKIPPED MODULES" final-report section
	// (like a check that did not pass) so they survive the live tree collapsing
	// on a successful run. (bool)
	GenerateSkippedAttr = "dagger.io/generate.skipped"

	// DagBlockedAttr marks a lazy-evaluation resume span that aborted because a
	// prerequisite result's evaluation failed, rather than because the result's
	// own deferred work failed. The UI treats a blocked resumption as if the
	// deferred work never ran: the owning API spans return to pending instead
	// of being marked caused-failed.
	DagBlockedAttr = "dagger.io/dag.blocked"

	// LLMCallDigestAttr is set on LLM prompt/response telemetry spans. Its
	// value is the DAG digest of the corresponding withPrompt or withResponse
	// call, enabling the TUI to branch from that point in the conversation.
	// (string)
	LLMCallDigestAttr = "dagger.io/llm.call.digest"

	// LLMToolResultTokensAttr is set on a tool-call telemetry span with an
	// estimated token count for the result the tool fed back into the model's
	// context. It lets the TUI flag tool calls whose (often huge) output is
	// the biggest driver of context growth, so an inordinate one is easy to
	// spot in a conversation. The count is an estimate (chars/4), not a
	// provider-reported figure. (int64)
	LLMToolResultTokensAttr = "dagger.io/llm.tool.result_tokens"

	// Streaming progress over OTel logs.
	//
	// A log record carrying ProgressItemAttr is progress data, not log text:
	// it reports absolute completion for one named item of work (a layer
	// being fetched, a file being transferred) within the span the record is
	// attached to. The TUI folds these records into progress bars instead of
	// rendering them as logs.
	//
	// Records are keyed by (span, item): each new record replaces the item's
	// previous state, so emitters can throttle freely and consumers only keep
	// the latest values.

	// ProgressItemAttr uniquely names the item within its span, e.g. a layer
	// digest. (string)
	ProgressItemAttr = "dagger.io/progress.item"
	// ProgressCurrentAttr is the item's absolute completed amount. (int64)
	ProgressCurrentAttr = "dagger.io/progress.current"
	// ProgressTotalAttr is the item's expected final amount. Zero or absent
	// means the total is unknown (indeterminate). (int64)
	ProgressTotalAttr = "dagger.io/progress.total"
	// ProgressUnitAttr optionally names the unit of current/total, e.g.
	// "bytes", for human-readable display. (string)
	ProgressUnitAttr = "dagger.io/progress.unit"
)

// wcprof × OTel vocabulary.
//
// These attributes let the engine emit, on its ordinary OTel spans, the
// wait-edge / shared-execution / causal-parent information that the native
// wcprof recorder records inline (engine/wcprof). They are the *only* new
// vocabulary the OTel profiling source introduces; everything else reuses
// existing dagger.io/* attributes. A single definition is shared by the
// offline analyzer and the engine emit sites, so the two can never diverge on a
// key or an encoding. Three conventions organize the vocabulary below: the
// wait-edge wire format (a wait is a link on the WAITER carrying the target op
// and the blocked interval, never a fan-in of links on the target); the
// target-before-primitive ordering that guarantees a joiner always reads a valid
// wait target; and the causal-parent override that lets lazy-re-pointed work keep
// its UI parent while naming its real cause.
const (
	// WcprofOpKindAttr (string) carries the wcprof op kind for a span when the
	// engine knows it, so the loader classifies the op without guessing —
	// e.g. "call_exec", "lazy", "service_start", "exec", "internal", "io". When
	// present it always wins over the loader's structural classification.
	WcprofOpKindAttr = "wcprof.op.kind"

	// WcprofWorkTypeAttr (string) coarsely attributes an op's self-time so
	// analysis can separate engine overhead from user workload and external
	// I/O: one of "engine", "user", "external". Absent ⇒ "engine".
	WcprofWorkTypeAttr = "wcprof.work_type"

	// WcprofParentAttr (string) is an explicit *causal*-parent override for a
	// span whose parentId is deliberately a non-causal UI parent (the lazy
	// re-point: deferred work keeps rendering under the producer call that returned
	// it, a span that already ended, so parentId there cannot be its cause). Its
	// value is the causal parent's OTel span
	// id encoded as the lower-hex string hex.EncodeToString(spanID[:]) — the
	// 16-char form spans/links use on the wire — so the stamping span processor
	// and the loader cannot diverge on encoding. The loader's causal parent is
	// WcprofParentAttr ?? parentId; it only ever *reads* this, never derives it.
	WcprofParentAttr = "wcprof.parent"

	// Wait-edge link attributes. A wait edge is emitted as a span link on the
	// *waiter*'s span carrying LinkPurposeAttr=LinkPurposeWait, plus these.
	// Timestamps are absolute Unix nanoseconds encoded as decimal strings: the
	// engine only knows wall-clock at emit time (the trace epoch is unknowable
	// until all spans are ingested, so the loader rebases), and decimal strings
	// round-trip exactly through Cloud's map[string]any JSON decode where a
	// number would be coerced to float64 and lose nanosecond precision above
	// 2^53.
	//
	// WcprofWaitStartUnixNanoAttr / WcprofWaitEndUnixNanoAttr bound the blocked
	// interval (decimal-string absolute Unix nanos).
	WcprofWaitStartUnixNanoAttr = "wcprof.wait.start_unix_ns"
	WcprofWaitEndUnixNanoAttr   = "wcprof.wait.end_unix_ns"
	// WcprofWaitReasonAttr (string) names why the waiter blocked: one of
	// "singleflight", "call_exec", "lazy", "service", "lock", "exec", "io".
	WcprofWaitReasonAttr = "wcprof.wait.reason"
	// WcprofWaitIdentAttr (string) names the awaited resource for waits that
	// have no target span (reason "lock"), in place of the link's target span id.
	WcprofWaitIdentAttr = "wcprof.wait.ident"

	// WcprofExecArgvAttr (string) carries the user command of a container-exec
	// processRun span so the offline analyzer ranks the exec by its real argv
	// (e.g. "go build") instead of the anonymous exec.processRun blob. Its value
	// is the scrubbed, bounded argv encoded as a single scalar JSON-array string
	// (e.g. `["go","build","./..."]`) — the SAME bytes the native recorder interns
	// as its MetaID, so both sources reconstruct an identical argv (cross-source
	// parity). It is a scalar string (not an OTLP array) so it survives Cloud's
	// map[string]any attribute decode bit-exact, like the decimal-string wait
	// timings above.
	WcprofExecArgvAttr = "wcprof.exec.argv"

	// LinkPurposeWait is a new value for telemetry.LinkPurposeAttr
	// ("dagger.io/link.purpose"), alongside the existing "cause"/"error_origin"
	// (defined in github.com/dagger/otel-go, which this repo cannot edit). It
	// marks a span link as a runtime wait edge for the wcprof analyzer.
	LinkPurposeWait = "wait"

	// Completeness checksum (leaf-drop detection). The reference-based
	// gate signals (OrphanedParents/UnresolvedWaitTargets) catch loss that breaks an
	// EDGE, but a dropped LEAF span that nothing references leaves no evidence — so a
	// large Cloud trace with the residual CLI→Cloud export drop could gate-pass while
	// silently incomplete. The producer therefore declares how many spans it emitted,
	// and the loader refuses a trace that received fewer (faithful data or refuse,
	// never a wrong answer). A dropped leaf is otherwise undetectable from the trace.

	// WcprofEngineSpanAttr (bool true) marks a span the ENGINE emitted for a trace —
	// the counted, ranking-critical population. Stamped at span creation by the
	// engine's per-client span-count processor (engine/server). The loader counts
	// these to reconcile against the declared total; CLI-shell and HTTP/buildkit
	// spans (which the engine does not count) are unmarked and excluded, so they
	// cannot cause a false pass/fail.
	WcprofEngineSpanAttr = "wcprof.engine_span"

	// WcprofSessionSpanCountAttr (string-encoded int, like the other wcprof numeric
	// attrs) is the EXACT TOTAL number of engine spans the engine emitted for this
	// trace, stamped at SESSION TEARDOWN on the WcprofSessionCompleteAttr carrier
	// span — after every query is drained and every service stopped, so it is the
	// final total, not a running floor. The loader compares the count of received
	// WcprofEngineSpanAttr spans to this declared total: because the declaration is
	// the exact final (received <= total always), received < total ⇒ spans dropped ⇒
	// hard-fail; absent ⇒ unverifiable ⇒ hard-fail (fail-by-default).
	WcprofSessionSpanCountAttr = "wcprof.session_span_count"

	// WcprofSessionCompleteAttr (bool true) marks the dedicated session-teardown
	// carrier span that declares WcprofSessionSpanCountAttr. It is a pure count
	// messenger, NOT a unit of work and NOT a counted engine span: the producer's
	// span-count processor skips it (so it is excluded from the total it carries —
	// no chicken-and-egg) and the loader drops it from the compiled ops (so the
	// graph/replay is untouched) after reading its count.
	WcprofSessionCompleteAttr = "wcprof.session_complete"
)
