package dagui

import (
	"fmt"
	"sort"
	"time"

	"github.com/dagger/dagger/engine/agentcontrol"
)

// The client half of the agent directory (hack/designs/async-agents.md §3.3).
//
// Async agents have no session-wide namespace on purpose — you can only
// message an agent whose ID you hold — so a client discovers the agents in a
// session by folding the trace it already ingests, rather than by querying
// for them. The engine publishes each runtime as a long-lived loop span
// carrying its identity, plus revisioned control records (engine/agentcontrol)
// over the log stream; everything below is the consumer of that contract.

// AgentNode is one agent instance in the session's roster: the loop span (or
// spans) the engine published for it, the identity carried on them, and the
// lifecycle state of its selected control revision.
type AgentNode struct {
	// Control is the selected complete revision, and the only source of the
	// lifecycle fields below. Nil for an agent seen only through its loop
	// spans, which is displayable but not restorable.
	Control *agentcontrol.Agent

	// ID is the agent's spawn-minted runtime handle — the grouping key. It is
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

	// State is the agent's lifecycle state, and WaitingOn what it is parked
	// on when that state is WAITING_INPUT.
	State     string
	WaitingOn string

	// StopReason says what ended a STOPPED agent: EXPLICIT for a stop
	// somebody asked for, SESSION for the one session teardown performs on
	// every surviving runtime. The projection is STOPPED either way, so this
	// is the only thing telling a dismissal apart from a clean exit.
	StopReason string

	// SnapshotDigest is the portable recipe digest of the agent's last committed
	// conversation — the resume anchor. A client rebuilds that conversation's
	// ID from the call payloads it has ingested and re-hydrates the instance
	// from it.
	SnapshotDigest string

	// PreTeardownState is the state held before a session-teardown stop, and
	// what a restore puts back when State is a teardown STOPPED.
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
	if node.Control != nil && (span == nil || span.TraceID.String() != node.Control.Trace) {
		return false
	}
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
	}

	// Lifecycle comes only from control revisions: select one per handle,
	// preferring the live session's incarnation over imported ones.
	selected := map[string]agentcontrol.Agent{}
	live := db.liveTraceID().String()
	for _, projection := range db.agentControl.Agents() {
		old, exists := selected[projection.Handle]
		if exists {
			if old.Trace == live && projection.Trace != live {
				continue
			}
			if projection.Trace != live && !projection.Activity.After(old.Activity) {
				continue
			}
		}
		selected[projection.Handle] = projection
	}
	for handle, projection := range selected {
		node := byID[handle]
		if node == nil {
			node = &AgentNode{ID: handle}
			byID[handle] = node
			order = append(order, node)
		}
		node.Control = &projection
		node.Name, node.CallDigest = projection.Name, projection.CallDigest
		node.State, node.WaitingOn, node.StopReason = projection.State, projection.WaitingOn, projection.StopReason
		node.SnapshotDigest, node.PreTeardownState = projection.Digest, projection.PreTeardownState
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i].Spans, order[j].Spans
		if len(a) == 0 && len(b) == 0 {
			return order[i].ID < order[j].ID
		}
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
	// Source identifies the control revision's ordering domain. Zero for an
	// agent with no control record, which is never restorable.
	Source agentcontrol.Key
	// ID is the agent's spawn-minted runtime handle — the identity it is
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

	// Error is the loop error a FAILED agent carries, from its control
	// record. Empty for every other state.
	Error string

	// SnapshotDigest is the portable recipe digest of the agent's last committed
	// conversation: the anchor the client rebuilds an ID from
	// (DB.CallIDForDigest) and re-hydrates the instance through.
	SnapshotDigest string

	// ParentAgentID is the runtime handle of the agent that spawned this one
	// — a worker's chief. Empty for a top-level agent, which is the fact
	// focus selection is made of (§3.1c).
	ParentAgentID string

	// LastActivity is when this agent was last seen doing anything. It
	// exists for the other half of §3.1c's focus rule — several top-level
	// agents means focusing the most recently ACTIVE one, which roster order
	// cannot answer, since that orders by when each agent first appeared.
	LastActivity time.Time

	// Err says why this entry cannot be restored: the trace has no control
	// record for it, or its record carries a capture failure or incomplete
	// lifecycle facts. These are refusals rather than guesses (§3.2, §4.4).
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

// RestorePlan projects the trace's agents into what a resuming client needs
// to re-hydrate them, in roster order.
//
// Deliberately a projection, not a query: async-agents §3.3 renounced
// Query.agents because telemetry is the directory, and resume keeps that
// property — an agent whose spans a client cannot see stays unreachable to
// it. The §3.1 state mapping is agentcontrol.Agent.RestoreState, shared with
// archive verification so it cannot drift between callers.
//
// Agents this session already holds are left out: an agent with a loop or
// identity span in the LIVE trace has a runtime entry here already, whether
// it was spawned in this session or restored into it earlier, and
// re-hydrating it is precisely what LLM.spawn(handle:) refuses. That is what
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
	if node.Control != nil && node.Control.Trace == traceID.String() {
		return true
	}
	for _, span := range node.Spans {
		if span.TraceID == traceID {
			return true
		}
	}
	return false
}

func (node *AgentNode) restoreEntry() AgentRestore {
	a := node.Control
	if a == nil {
		return AgentRestore{ID: node.ID, Name: node.Name,
			Err: fmt.Errorf("agent %q (%s) has no control record: "+
				"this trace predates agent control records, so it carries nothing to restore it from",
				node.Name, node.ID)}
	}
	return RestoreEntryFromControl(*a)
}
