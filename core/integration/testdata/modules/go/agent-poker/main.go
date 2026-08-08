// A module whose tool messages the agent that called it, exercising Agent!
// argument injection (hack/designs/async-agents.md §3.1): when poke is
// dispatched as a tool from a running agent loop, the engine auto-injects
// the calling agent's handle into the parent argument, which is hidden from
// the tool schema the model sees.
package main

import (
	"context"

	"dagger/poker/internal/dagger"
)

type Poker struct{}

// Poke fire-and-forgets a note to the calling agent — the child->parent
// channel — and returns the delivery evidence of that self-send. It must
// never await the reply: the note joins the very turn this tool call is part
// of, so awaiting would deadlock (the self-await hazard, design §9).
//
// (The injected argument is named caller rather than parent because the Go
// SDK's generated dispatch code reserves `parent` for the receiver.)
func (m *Poker) Poke(ctx context.Context, caller *dagger.Agent, note string) (string, error) {
	msgID, err := caller.Send(ctx, note)
	if err != nil {
		return "", err
	}
	// Send returns the pinned message ID (the enqueue already happened);
	// loading it replays the message(id:) lookup, not the send.
	delivery, err := dagger.Ref[*dagger.AgentMessage](dag, msgID).Delivery(ctx)
	if err != nil {
		return "", err
	}
	return "delivery: " + string(delivery), nil
}
