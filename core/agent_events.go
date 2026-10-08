package core

import (
	"context"
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"
)

// AgentEvent is one projected lifecycle transition of an agent's runtime
// entry, as read by code through Subscription.agentEvents or the
// Agent.events pull twin (hack/designs/graphql-subscriptions.md §4.1).
//
// It is the same edge Agent.notify fans out to subscribing agents
// (queueEventsLocked), with a sink for code instead of a mailbox: every
// edge transitionLocked detects is appended to the entry's transition log
// with a 1-based seq. The value is identity-light — its ID is the honest
// chain …agent(handle:…).event(seq: n), which re-reads the live log.
type AgentEvent struct {
	Seq           int        `field:"true" doc:"1-based position in this runtime entry's transition log."`
	State         AgentState `field:"true" doc:"The state the agent transitioned into."`
	TurnCompleted bool       `field:"true" name:"turnCompleted" doc:"An IDLE edge that completed a turn with newly committed work: exactly the IDLE transitions Agent.notify announces."`
	Reply         string     `field:"true" doc:"The completed turn's final reply, for an IDLE event; empty otherwise."`
	Error         string     `field:"true" doc:"The loop error, for a FAILED event; empty otherwise."`
	Text          string     `field:"true" doc:"The event text Agent.notify delivers to a subscribing agent for this transition."`

	// AgentHandle names the runtime entry the event belongs to.
	AgentHandle string
}

func (*AgentEvent) Type() *ast.Type {
	return &ast.Type{
		NamedType: "AgentEvent",
		NonNull:   true,
	}
}

func (*AgentEvent) TypeDescription() string {
	return "EXPERIMENTAL: Agent APIs are likely to change.\n\n" +
		"One lifecycle transition of an agent, in its runtime entry's ordered transition log."
}

func (e *AgentEvent) Clone() *AgentEvent {
	cp := *e
	return &cp
}

// agentTransition is one entry of a runtime's transition log.
type agentTransition struct {
	state         AgentState
	turnCompleted bool
	reply         string
	err           string
	text          string
}

// recordTransitionLocked appends one projection edge to the transition log.
// Must be called with rt.mu held, from transitionLocked (or create, for the
// initial state), BEFORE queueEventsLocked consumes idleEventDue.
func (rt *AgentRuntime) recordTransitionLocked(state AgentState) {
	tr := agentTransition{
		state:         state,
		turnCompleted: state == AgentStateIdle && rt.idleEventDue,
		text:          rt.eventTextLocked(state),
	}
	switch state {
	case AgentStateIdle:
		if last := rt.last.Self(); last != nil {
			if reply, found := last.LastReply(); found {
				tr.reply = reply
			}
		}
	case AgentStateFailed:
		if rt.loopErr != nil {
			tr.err = rt.loopErr.Error()
		}
	}
	rt.transitions = append(rt.transitions, tr)
}

func (rt *AgentRuntime) eventLocked(seq int) *AgentEvent {
	tr := rt.transitions[seq-1]
	return &AgentEvent{
		Seq:           seq,
		State:         tr.state,
		TurnCompleted: tr.turnCompleted,
		Reply:         tr.reply,
		Error:         tr.err,
		Text:          tr.text,
		AgentHandle:   rt.key,
	}
}

// Event returns the transition with the given seq.
func (rt *AgentRuntime) Event(seq int) (*AgentEvent, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if seq < 1 || seq > len(rt.transitions) {
		return nil, fmt.Errorf("agent %q has no event #%d in this session (latest is #%d)", rt.name, seq, len(rt.transitions))
	}
	return rt.eventLocked(seq), nil
}

// LatestEventSeq returns the seq of the transition into the current state.
func (rt *AgentRuntime) LatestEventSeq() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return len(rt.transitions)
}

// EventsAfter blocks until at least one transition with seq > after exists,
// then returns the seqs of every retained one. It reads under rt.mu and waits
// on stateChanged — the channel transitionLocked closes on every change —
// so no transition can fall between the read and the wait.
func (rt *AgentRuntime) EventsAfter(ctx context.Context, after int) ([]int, error) {
	if after < 0 {
		after = 0
	}
	for {
		rt.mu.Lock()
		n := len(rt.transitions)
		ch := rt.stateChanged
		rt.mu.Unlock()
		if n > after {
			seqs := make([]int, 0, n-after)
			for seq := after + 1; seq <= n; seq++ {
				seqs = append(seqs, seq)
			}
			return seqs, nil
		}
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-ch:
		}
	}
}

// RefuseAgentTurnSubscription refuses a blocking event read issued from
// inside an agent's turn (hack/designs/agent-messaging.md §3: a turn never
// blocks on another agent's progress), with the error that teaches the fix.
func RefuseAgentTurnSubscription(ctx context.Context, target *AgentRuntime, verb string) error {
	caller, ok := CallerAgent(ctx)
	if !ok {
		return nil
	}
	return fmt.Errorf(
		"agent %q cannot %s agent %q from within its own turn: a turn never blocks on another agent — an agent waits by ending its turn. Use notify(subscriber: %q) on %q, and its lifecycle events arrive as messages",
		caller.Self().Name, verb, target.name, caller.Self().Name, target.name)
}
