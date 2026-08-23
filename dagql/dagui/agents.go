package dagui

import (
	"fmt"
	"sort"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// The client half of the agent directory (hack/designs/async-agents.md §3.3).
//
// Async agents have no session-wide namespace on purpose — you can only
// message an agent whose ID you hold — so a client discovers the agents in a
// session by folding the trace it already ingests, rather than by querying
// for them. The engine publishes each runtime as a long-lived loop span
// carrying its identity, plus state records over the log stream; everything
// below is the consumer of that contract.

// AgentNode is one agent instance in the session's roster: the loop span (or
// spans) the engine published for it, the identity carried on them, and the
// lifecycle state folded from its state records.
type AgentNode struct {
	// ID is the agent's spawn-minted instance ID — the grouping key. It is
	// deliberately NOT the span ID: a resume after a failure relaunches the
	// loop under a fresh span, and both spans belong to the same agent.
	ID string

	// Name is the agent's display label. It carries no identity: two agents
	// may legitimately share one.
	Name string

	// CallDigest is the digest of the call that produced the agent value,
	// for reconstructing a sendable handle. Empty when the engine could not
	// derive it, in which case the agent is observable but not addressable.
	CallDigest string

	// Spans are the agent's loop spans, oldest first. The last is current.
	Spans []*Span

	// State is the agent's lifecycle state as of the most recent state
	// record, and WaitingOn what it is parked on when that state is
	// WAITING_INPUT.
	State     string
	WaitingOn string

	// StopReason says what ended a STOPPED agent: EXPLICIT for a stop
	// somebody asked for, SESSION for the one session teardown performs on
	// every surviving runtime. The projection is STOPPED either way, so this
	// is the only thing telling a dismissal apart from a clean exit — a
	// distinction a client restoring the trace cannot guess (restore both and
	// every dismissal is reversed; restore neither and a clean exit restores
	// nothing). Empty on a trace from an engine that predates it.
	StopReason string

	// SnapshotDigest is the portable recipe digest of the agent's last committed
	// conversation — the resume anchor. A client rebuilds that conversation's
	// ID from the call payloads it has ingested and re-hydrates the instance
	// from it.
	SnapshotDigest string

	// PreTeardownState is the last state the agent published that was not a
	// session-teardown stop, and it is what a restore puts back when State is
	// a teardown STOPPED: session close stops every surviving runtime, so the
	// trace's last word on a cleanly closed session's agents says nothing
	// about what the user wanted. Empty when the agent never published
	// anything else.
	PreTeardownState string
}

// Span returns the agent's current loop span — the one a caller should scope
// a view to, or read timing from.
func (node *AgentNode) Span() *Span {
	if len(node.Spans) == 0 {
		return nil
	}
	return node.Spans[len(node.Spans)-1]
}

// Live reports whether the agent's loop is currently running: its newest
// loop span has not ended. This is a fact about the SPAN, not the projected
// state — a paused agent is still live, and a stopped one is not.
func (node *AgentNode) Live() bool {
	span := node.Span()
	return span != nil && span.IsRunning()
}

// Agents returns every agent published into the trace, ordered by when each
// first appeared, so a roster keeps a stable order as agents come and go.
//
// Unlike SurfacedServices this is deliberately FLAT and unfiltered by
// Boundary/Encapsulate containment: an agent spawned deep inside a module
// call — a staff worker, born under its chief's tool-call span — is exactly
// the agent the roster exists to surface, so hiding contained ones would
// defeat the purpose. Nesting is left to the caller, which can derive it
// from the spans' ancestry when it wants a chief/worker tree.
//
// The result is cached per DB mutation; callers must treat the returned
// nodes as read-only.
func (db *DB) Agents() []*AgentNode {
	if db.agentsInit && db.agentsAt == db.mutations {
		return db.agents
	}
	db.agents = db.buildAgents()
	db.agentsAt = db.mutations
	db.agentsInit = true
	return db.agents
}

func (db *DB) buildAgents() []*AgentNode {
	byID := map[string]*AgentNode{}
	var order []*AgentNode
	for span := range db.Spans.Iter() {
		if !span.Agent || span.AgentID == "" {
			continue
		}
		node, ok := byID[span.AgentID]
		if !ok {
			node = &AgentNode{ID: span.AgentID}
			byID[span.AgentID] = node
			order = append(order, node)
		}
		node.Spans = append(node.Spans, span)
		// Identity is immutable, but a relaunched loop re-stamps it; take
		// the newest non-empty value so a re-spawned loop span can correct
		// a partially stamped predecessor.
		if span.AgentName != "" {
			node.Name = span.AgentName
		}
		if span.AgentCallDigest != "" {
			node.CallDigest = span.AgentCallDigest
		}
	}

	for _, node := range order {
		sort.SliceStable(node.Spans, func(i, j int) bool {
			return node.Spans[i].Before(node.Spans[j])
		})
		// State is latest-wins across the agent's loop spans, and the newest
		// span holds the newest records by construction. The snapshot digest
		// follows the same rule for the same reason: a resume-retry relaunch
		// re-publishes the conversation it picks back up, so the newest span
		// always carries an anchor.
		if span := node.Span(); span != nil {
			node.State = span.AgentState
			node.WaitingOn = span.AgentWaitingOn
			node.StopReason = span.AgentStopReason
			node.SnapshotDigest = span.AgentSnapshotDigest
		}
		// The pre-teardown state is the exception: it is the newest one that
		// EXISTS, not the newest span's. A relaunched loop whose only record
		// is the teardown stop would otherwise erase what the previous loop
		// left behind.
		for i := len(node.Spans) - 1; i >= 0; i-- {
			if state := node.Spans[i].AgentPreTeardownState; state != "" {
				node.PreTeardownState = state
				break
			}
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i].Spans, order[j].Spans
		if len(a) == 0 || len(b) == 0 {
			return len(a) > len(b)
		}
		return a[0].Before(b[0])
	})
	return order
}

// AgentRestore is one entry of a restore plan: what a resuming client needs
// to re-hydrate one agent instance from a trace it has ingested.
type AgentRestore struct {
	// ID is the agent's spawn-minted instance ID — the identity it is
	// restored UNDER, which is what makes the restored agent the same agent
	// (its old loop spans and its new ones fold into one roster entry, and
	// the chief's recorded chain binds its workers by this ID).
	ID string

	// Name is the display label to restore it with.
	Name string

	// State is the state to re-hydrate the instance into: IDLE, PAUSED,
	// FAILED or STOPPED, already mapped per design §3.1. Never RUNNING or
	// WAITING_INPUT — those describe a loop, and the loop died with the
	// session that published the trace.
	State string

	// Error is the loop error a FAILED agent carries, taken from its loop
	// span's status. Empty for every other state.
	Error string

	// SnapshotDigest is the portable recipe digest of the agent's last committed
	// conversation: the anchor the client rebuilds an ID from
	// (DB.CallIDForDigest) and re-hydrates the instance through.
	SnapshotDigest string

	// ParentAgentID is the instance ID of the agent whose loop span encloses
	// this one — a worker's chief. Empty for a top-level agent, which is the
	// fact focus selection is made of (§3.1c).
	ParentAgentID string

	// LastActivity is when this agent was last seen doing anything: the
	// newest end time across its loop spans, or their newest start when one
	// never ended. It exists for the other half of §3.1c's focus rule —
	// several top-level agents means focusing the most recently ACTIVE one,
	// which roster order cannot answer, since that orders by when each agent
	// first appeared. The two routinely disagree: a session's own
	// conversation is the first agent to appear and usually the last to
	// speak.
	LastActivity time.Time

	// Err says why this entry cannot be restored, when the trace does not
	// carry enough to restore it: a STOPPED record with no reason, or no
	// snapshot digest at all. Both are refusals rather than guesses (§3.2,
	// §4.4) — guessing a stop reason either loses a whole session or
	// resurrects every dismissal, and guessing an anchor restores an
	// amnesiac twin under the agent's own ID.
	//
	// The entry is still reported, carrying every fact that did resolve, so
	// a caller can name the agent in its failure and a best-effort restore
	// can skip exactly this one.
	Err error
}

// Restorable reports whether this entry can be re-hydrated: an entry with an
// Err is one the trace does not carry enough to restore, which fails the
// command rather than being restored on a guess.
func (entry AgentRestore) Restorable() bool {
	return entry.Err == nil
}

// The lifecycle state tokens as they ride the wire (core.AgentState). dagui
// consumes telemetry, not the engine's Go types, so they are spelled out
// here.
const (
	agentStateIdle         = "IDLE"
	agentStateRunning      = "RUNNING"
	agentStateWaitingInput = "WAITING_INPUT"
	agentStatePaused       = "PAUSED"
	agentStateStopped      = "STOPPED"
	agentStateFailed       = "FAILED"

	agentStopExplicit = "EXPLICIT"
	agentStopSession  = "SESSION"
)

// RestorePlan projects the trace's agents into what a resuming client needs
// to re-hydrate them, in roster order.
//
// Deliberately a projection, not a query: async-agents §3.3 renounced
// Query.agents because telemetry is the directory, and resume keeps that
// property — an agent whose spans a client cannot see stays unreachable to
// it. The §3.1 state mapping lives here, in one place, so it is testable
// without an engine and cannot drift between callers.
//
// Agents this session already holds are left out: an agent with a loop or
// identity span in the LIVE trace has a runtime entry here already, whether
// it was spawned in this session or restored into it earlier, and
// re-hydrating it is precisely what Agent.rehydrate refuses. That is what
// makes running a restore twice a no-op instead of a second re-hydration —
// and it takes the trace ID to see, because a re-hydrated agent republishes
// its identity, state and snapshot into the new trace (§4.5), so nothing
// else about its entry distinguishes it from a source-trace one.
func (db *DB) RestorePlan() []AgentRestore {
	live := db.liveTraceID()
	var plan []AgentRestore
	for _, node := range db.Agents() {
		if node.publishedIn(live) {
			continue
		}
		plan = append(plan, node.restoreEntry())
	}
	return plan
}

// liveTraceID is the trace this client's own session publishes into: the
// primary span's, or the root's when nothing set a primary. Both are the live
// CLI's by the time an import runs — a resume must not repoint them (§5.1.1)
// — so anything else in the DB was imported.
func (db *DB) liveTraceID() TraceID {
	if span, ok := db.Spans.Map[db.PrimarySpan]; ok && span != nil && span.TraceID.IsValid() {
		return span.TraceID
	}
	if db.RootSpan != nil {
		return db.RootSpan.TraceID
	}
	return TraceID{}
}

// publishedIn reports whether any of the agent's spans belong to the given
// trace. An invalid trace ID matches nothing: with no live session to compare
// against, every agent is foreign.
func (node *AgentNode) publishedIn(traceID TraceID) bool {
	if !traceID.IsValid() {
		return false
	}
	for _, span := range node.Spans {
		if span.TraceID == traceID {
			return true
		}
	}
	return false
}

func (node *AgentNode) restoreEntry() AgentRestore {
	entry := AgentRestore{
		ID:             node.ID,
		Name:           node.Name,
		SnapshotDigest: node.SnapshotDigest,
		ParentAgentID:  node.parentAgentID(),
		LastActivity:   node.lastActivity(),
	}
	state, err := node.restoreState()
	if err != nil {
		entry.Err = err
		return entry
	}
	entry.State = state
	if state == agentStateFailed {
		entry.Error = node.loopError()
	}
	if entry.SnapshotDigest == "" {
		entry.Err = fmt.Errorf("agent %q (%s) published no snapshot digest: "+
			"this trace predates %s, so there is no conversation to restore it from",
			node.Name, node.ID, telemetryattrs.AgentSnapshotDigestAttr)
	}
	return entry
}

// restoreState applies design §3.1's table to the agent's last state record.
func (node *AgentNode) restoreState() (string, error) {
	if node.State != agentStateStopped {
		// Everything that is not a stop restores from the record itself; a
		// missing record reads as a crash, which restores like RUNNING.
		return node.restorableState(node.State)
	}
	switch node.StopReason {
	case agentStopExplicit:
		// A stop somebody asked for is terminal on purpose (async-agents
		// §3.5): it restores as a sealed tombstone, whose snapshot stays
		// readable so a dismissed worker's WIP is still harvestable.
		return agentStateStopped, nil
	case agentStopSession:
		// Session close stops every surviving runtime, so this record says
		// nothing about what the user wanted. What they left behind is the
		// state held before the teardown.
		return node.restorableState(node.PreTeardownState)
	default:
		return "", fmt.Errorf("agent %q (%s) published a STOPPED record with no %s: "+
			"this trace predates it, so a dismissal cannot be told from the stop session teardown performs",
			node.Name, node.ID, telemetryattrs.AgentStopReasonAttr)
	}
}

// restorableState maps a published state onto one the agent can be restored
// into.
//
// RUNNING, WAITING_INPUT and absence all become IDLE, and that is the one
// deliberate deviation from "exactly as it was": the loop died with its
// session, so a roster redisplaying it as running — or as parked on a
// question nothing will answer — would be lying (async-agents §3.4). What
// survives is the last committed step; the interrupted turn's input is still
// on the snapshot and re-steps when the agent is next prompted. Nothing
// auto-continues.
func (node *AgentNode) restorableState(state string) (string, error) {
	switch state {
	case "", agentStateIdle, agentStateRunning, agentStateWaitingInput:
		return agentStateIdle, nil
	case agentStatePaused, agentStateFailed:
		return state, nil
	case agentStateStopped:
		// Only reachable as a pre-teardown state, and only an explicit stop
		// is ever recorded as one — a session stop is exactly what that
		// field skips.
		return agentStateStopped, nil
	default:
		// A token from an engine newer than this client. Refuse it for the
		// same reason the two above are refused: every reading is a guess,
		// and the wrong one either resurrects or buries an agent.
		return "", fmt.Errorf("agent %q (%s) published state %q, which this client cannot restore",
			node.Name, node.ID, state)
	}
}

// loopError is the error a failed loop reported, from the newest loop span
// carrying one: the loop's own status description (core/agent.go sets it from
// loopErr), which is where a FAILED agent's error survives in the trace.
func (node *AgentNode) loopError() string {
	for i := len(node.Spans) - 1; i >= 0; i-- {
		span := node.Spans[i]
		if span.Status.Code == codes.Error && span.Status.Description != "" {
			return span.Status.Description
		}
	}
	return ""
}

// parentAgentID finds the agent enclosing this one, by walking each loop
// span's ancestry for another agent's loop span. A worker's loop is started
// under its chief's tool-call span, so nesting is readable from the DB
// (§3.1c) — and it is read from the OLDEST loop span first, because that is
// the one born under the chief; a resume-retry relaunch can happen anywhere.
func (node *AgentNode) parentAgentID() string {
	for _, span := range node.Spans {
		for parent := span.ParentSpan; parent != nil; parent = parent.ParentSpan {
			if parent.Agent && parent.AgentID != "" && parent.AgentID != node.ID {
				return parent.AgentID
			}
		}
	}
	return ""
}

// lastActivity is the newest timestamp any of the agent's loop spans carries.
//
// A span that never ended contributes its START time, not "now": an imported
// trace's unfinished spans are sealed to one shared bound at import
// (design §5.1.2), so ends alone would tie every agent of a crashed session
// together and lose the ordering entirely. Falling back to the start breaks
// that tie by when each loop last (re)launched, which is the best the trace
// can say about a session nothing recorded the end of.
func (node *AgentNode) lastActivity() time.Time {
	var newest time.Time
	for _, span := range node.Spans {
		for _, at := range []time.Time{span.StartTime, span.EndTime} {
			if at.After(newest) {
				newest = at
			}
		}
	}
	return newest
}

// ingestAgentState folds an agent-state log record (one carrying
// telemetryattrs.AgentStateAttr) into the target span. It reports whether the
// record was agent state; such records are consumed entirely and must not be
// treated as log text — they are data about the agent, not output from it.
//
// Latest record wins, including for WaitingOn and StopReason: the engine emits
// both as an explicit empty string on every record they do not apply to, so a
// question that has since been answered — or the reason of a stop a resume
// retried past — is cleared rather than left standing.
//
// The one thing kept from the record HISTORY is the state before a
// session-teardown stop, because the projection a restore needs cannot be
// computed from the latest record alone: teardown stops every surviving
// runtime, so its record overwrites the state the user actually left the
// agent in (see AgentNode.PreTeardownState).
func (db *DB) ingestAgentState(record sdklog.Record) bool {
	var state, waitingOn, stopReason string
	var reserved, validState bool
	record.WalkAttributes(func(kv otellog.KeyValue) bool {
		switch kv.Key {
		case telemetryattrs.AgentStateAttr:
			reserved = true
			state, validState = LogValueString(kv.Value)
		case telemetryattrs.AgentWaitingOnAttr:
			if value, ok := LogValueString(kv.Value); ok {
				waitingOn = value
			}
		case telemetryattrs.AgentStopReasonAttr:
			if value, ok := LogValueString(kv.Value); ok {
				stopReason = value
			}
		}
		return true
	})
	if !reserved {
		return false
	}
	if !validState {
		return true
	}

	spanID := SpanID{SpanID: record.SpanID()}
	if !spanID.IsValid() {
		return true
	}
	span := db.initSpan(spanID)
	if state != agentStateStopped || stopReason != agentStopSession {
		span.AgentPreTeardownState = state
	}
	span.AgentState = state
	span.AgentWaitingOn = waitingOn
	span.AgentStopReason = stopReason
	// The roster is derived from spans, so a state change is a DB mutation
	// like any other; without this the memoized Agents() would go stale.
	db.mutations++
	return true
}

// ingestAgentSnapshot folds an agent-snapshot log record (one carrying
// telemetryattrs.AgentSnapshotDigestAttr) into the target span, reporting
// whether the record was one. Like state records these are consumed as data
// and never rendered as log text.
//
// They are a separate channel from state because they are triggered by a
// different thing: the engine emits one on every commit of the agent's
// conversation, where state records are edge-triggered on the projected state
// and most commits do not move it. Latest record wins — this is the tip.
func (db *DB) ingestAgentSnapshot(record sdklog.Record) bool {
	var digest string
	var reserved, validDigest bool
	record.WalkAttributes(func(kv otellog.KeyValue) bool {
		if kv.Key == telemetryattrs.AgentSnapshotDigestAttr {
			reserved = true
			digest, validDigest = LogValueString(kv.Value)
		}
		return true
	})
	if !reserved {
		return false
	}
	if !validDigest {
		return true
	}

	spanID := SpanID{SpanID: record.SpanID()}
	if !spanID.IsValid() {
		return true
	}
	db.initSpan(spanID).AgentSnapshotDigest = digest
	db.mutations++
	return true
}
