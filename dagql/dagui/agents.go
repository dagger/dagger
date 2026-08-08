package dagui

import (
	"sort"

	"github.com/dagger/dagger/engine/telemetryattrs"
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
		// span holds the newest records by construction.
		if span := node.Span(); span != nil {
			node.State = span.AgentState
			node.WaitingOn = span.AgentWaitingOn
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

// ingestAgentState folds an agent-state log record (one carrying
// telemetryattrs.AgentStateAttr) into the target span. It reports whether the
// record was agent state; such records are consumed entirely and must not be
// treated as log text — they are data about the agent, not output from it.
//
// Latest record wins, including for WaitingOn: the engine emits it as an
// explicit empty string for every state other than WAITING_INPUT, so a
// question that has since been answered is cleared rather than left standing.
func (db *DB) ingestAgentState(record sdklog.Record) bool {
	var state, waitingOn string
	var sawState bool
	record.WalkAttributes(func(kv otellog.KeyValue) bool {
		switch kv.Key {
		case telemetryattrs.AgentStateAttr:
			state = kv.Value.AsString()
			sawState = true
		case telemetryattrs.AgentWaitingOnAttr:
			waitingOn = kv.Value.AsString()
		}
		return true
	})
	if !sawState {
		return false
	}

	spanID := SpanID{SpanID: record.SpanID()}
	if !spanID.IsValid() {
		return true
	}
	span := db.initSpan(spanID)
	span.AgentState = state
	span.AgentWaitingOn = waitingOn
	// The roster is derived from spans, so a state change is a DB mutation
	// like any other; without this the memoized Agents() would go stale.
	db.mutations++
	return true
}
