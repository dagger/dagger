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

// Cache-evidence contract (dagger.io/cache.*).
//
// These attributes record, on the ordinary per-call span the engine already
// emits, the cache-decision facts that otherwise exist only transiently inside
// dagql's lookup: what the cache decided (outcome/route), why a miss missed
// (expiry, session-resource filtering, unknown input), and the engine-derived
// structural identity of the call (self digest, ordered structural inputs,
// pairing digest, recorded output content digest). They exist so a trace
// consumer can explain cache non-reuse from recorded facts instead of
// re-deriving engine internals from span shapes.
//
// Wire shape: every value is an OTLP STRING, following the wcprof×OTel
// precedent above and for the same reason — Cloud ingestion JSON-encodes each
// attribute value into a ClickHouse Map(String,String) and the read path
// re-decodes heuristically (bare or quoted true/false become bools,
// leading-digit values become numbers, quoted strings round-trip as strings).
// The value tokens below are chosen so that trip is loss-free: enum tokens and
// digest values (algorithm-prefixed) can never collide with true/false/null or
// a leading digit; the two boolean facts are emitted as "true" only when true
// (absent means false) and intentionally decode into real bools; the
// unknown-input index and the structural-input list are emitted as a
// decimal-string and a single JSON-array-encoded string (the WcprofExecArgvAttr
// pattern) whose leading '[' keeps them strings end to end.
//
// Producer gating: the attributes are stamped by core.AroundFunc's completion
// callback from a request-only evidence carrier (dagql.CacheDecision) that
// core allocates only when the call's span records and the call is not
// ProfileSkip-classified — so suppressed, deduplicated, introspection and
// profile-skipped calls record nothing, and no new spans are ever created.
const (
	// CacheContractAttr is the producer-contract version marker. Consumers must
	// read cache.* facts only from spans carrying this marker and treat unknown
	// versions as not eligible. Semantics of every fact within a version are
	// frozen once released; new optional facts may be added under the same
	// version (presence-detected); any semantic change to an existing fact
	// requires bumping the version. Stamped whenever the evidence carrier
	// stamps, so it governs every other cache.* attribute on the span.
	CacheContractAttr = "dagger.io/cache.contract"
	// CacheContractV1 is the current (and first) contract version value.
	CacheContractV1 = "1"

	// CacheOutcomeAttr is what the cache decided for this call: one of
	// CacheOutcomeHit (reused a cached result), CacheOutcomeExecuted (miss —
	// the resolver ran), CacheOutcomeJoined (deduplicated into concurrent
	// identical in-flight work under the same concurrency key), or
	// CacheOutcomeUncached (DoNotCache policy: no lookup, no dedupe, no
	// publication). A hit on a still-pending lazy shell stamps
	// CacheOutcomeHit — the lookup fact — while the separate evaluation facts
	// (dagger.io/dag.cached, dagger.io/dag.pending) keep their existing
	// meaning.
	CacheOutcomeAttr     = "dagger.io/cache.outcome"
	CacheOutcomeHit      = "hit"
	CacheOutcomeExecuted = "executed"
	CacheOutcomeJoined   = "joined"
	CacheOutcomeUncached = "uncached"

	// CacheHitRouteAttr (hits only) is how the hit was found:
	// CacheHitRouteRecipe (exact recipe-digest match), CacheHitRouteDigest
	// (extra-digest equivalence, e.g. a content digest carried by the request),
	// or CacheHitRouteStructural (structural term over the call's self digest
	// and its inputs' equivalence classes).
	CacheHitRouteAttr       = "dagger.io/cache.hit.route"
	CacheHitRouteRecipe     = "recipe"
	CacheHitRouteDigest     = "digest"
	CacheHitRouteStructural = "structural"

	// CacheMissIncompatibleCandidatesAttr ("true", executed misses only, absent
	// otherwise) records that non-expired candidate results existed but none
	// satisfied this session's resource requirements (secrets/sockets the
	// session has not loaded). It is an existence flag, not a count: expiry is
	// applied during candidate accumulation, so on a miss a non-empty candidate
	// set means every surviving candidate failed the session-resource filter.
	CacheMissIncompatibleCandidatesAttr = "dagger.io/cache.miss.incompatible_candidates"

	// CacheMissSawExpiredAttr ("true", executed misses only, absent otherwise)
	// records that TTL expiry eliminated at least one otherwise-matching result
	// during candidate accumulation.
	CacheMissSawExpiredAttr = "dagger.io/cache.miss.saw_expired"

	// CacheMissUnknownInputAttr (executed misses only) is the decimal-string
	// index into this span's CacheStructuralInputsAttr list of the first input
	// digest that had no equivalence class at lookup time, which made the
	// structural (equivalence) lookup impossible. The semantics are
	// deliberately narrow: "equivalence lookup was skipped because this input
	// digest was unknown to the cache at that moment" — digest knowledge is
	// current cache state, not history, so this does NOT mean "first run".
	// Absent when every input was known.
	CacheMissUnknownInputAttr = "dagger.io/cache.miss.unknown_input"

	// CacheSelfDigestAttr is the engine-derived structural self digest of the
	// call: the operation, its literal arguments (sensitive values redacted),
	// implicit inputs, list selection and schema view — with reference-valued
	// inputs factored out into CacheStructuralInputsAttr. Two calls with equal
	// self digests are "the same operation over possibly different inputs".
	CacheSelfDigestAttr = "dagger.io/cache.self_digest"

	// CacheStructuralInputsAttr is the exact ordered structural-input digest
	// list the engine's equivalence lookup keys on — receiver, reference-valued
	// arguments in argument order, digest-witnessed strings, then the module
	// reference — encoded as a single JSON-array-of-strings value (the
	// WcprofExecArgvAttr pattern). This is the list CacheMissUnknownInputAttr
	// indexes into. It is a different projection than dagger.io/dag.inputs
	// (which deduplicates and omits the module), and that attribute is
	// unchanged.
	CacheStructuralInputsAttr = "dagger.io/cache.structural_inputs"

	// CachePairingDigestAttr is the self digest computed with implicit inputs
	// excluded: "this operation, these literal arguments, this view/selection —
	// over whatever inputs, in whatever scope". It is the engine-authored
	// cross-run pairing anchor: equal pairing digests are counterpart
	// CANDIDATES for run-to-run comparison (an index, not proof), so
	// per-client/per-session implicit inputs can never break candidate
	// discovery, while remaining visible by name in dagger.io/dag.call. For a
	// call with no implicit inputs it equals CacheSelfDigestAttr.
	CachePairingDigestAttr = "dagger.io/cache.pairing_digest"

	// CacheOutputContentDigestAttr is the completed result's RECORDED content
	// digest — the last content-labeled extra digest on the result's
	// authoritative call frame at span completion — for hits and executions
	// alike, emitted only when non-empty. It is deliberately the recorded fact
	// only (never the derived content-preferred digest): absence means "no
	// recorded content identity at completion", nothing more. A lazy result may
	// gain its content digest only after this span ends; that later fact is
	// simply not claimed here.
	CacheOutputContentDigestAttr = "dagger.io/cache.output.content_digest"
)
