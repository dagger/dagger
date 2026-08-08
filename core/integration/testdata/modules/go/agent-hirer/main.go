// A module that spawns an agent INSIDE a module function and keeps the
// handle to itself — the roster's headline case
// (hack/designs/async-agents.md §3.3): an agent the calling client never
// spawned and holds no ID for.
package main

import (
	"context"

	"dagger/hirer/internal/dagger"
)

// WorkerPrompt is composed into every hired agent, so the spawned value's
// chain carries a frame built inside this module. Asserted verbatim by
// core/integration/agent_runtime_test.go.
const WorkerPrompt = "You are a worker hired by the hirer module."

type Hirer struct{}

// Hire extends the caller's seed here, spawns an agent from it, sends it one
// message, and returns only the send's delivery evidence. The agent ID is
// deliberately NOT returned: the caller can reach the agent only through what
// the trace advertises about it.
func (m *Hirer) Hire(ctx context.Context, seed *dagger.LLM, name string, task string) (string, error) {
	agentID, err := seed.WithSystemPrompt(WorkerPrompt).
		Spawn(ctx, dagger.LLMSpawnOpts{Name: name})
	if err != nil {
		return "", err
	}
	msgID, err := dagger.Ref[*dagger.Agent](dag, agentID).Send(ctx, task)
	if err != nil {
		return "", err
	}
	// Send returns the pinned message ID (the enqueue already happened);
	// loading it replays the message(id:) lookup, not the send.
	delivery, err := dagger.Ref[*dagger.AgentMessage](dag, msgID).Delivery(ctx)
	if err != nil {
		return "", err
	}
	return string(delivery), nil
}
