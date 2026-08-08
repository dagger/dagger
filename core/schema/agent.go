package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// agentSchema installs the Agent type: the async evaluation-loop entity of
// hack/designs/async-agents.md. The Agent value itself is constructed by
// LLM.asAgent (core/schema/llm.go); this file installs its lifecycle fields,
// all of which operate on the session-scoped runtime registry
// (core.AgentRuntimes) rather than on the value.
type agentSchema struct{}

var _ SchemaResolvers = &agentSchema{}

func (s agentSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Agent]{
		dagql.Func("name", s.name).
			Doc(`Display label and identity discriminator — not a session-wide address.`),

		dagql.NodeFunc("state", s.state).
			DoNotCache("Projects live runtime state.").
			Doc(`Computed lifecycle state; never stored.`,
				`An agent that was never started reports IDLE: its mailbox is empty and no turn is open.`),

		dagql.NodeFunc("snapshot", s.snapshot).
			DoNotCache("Reflects the loop's last committed step, which advances as the agent runs.").
			Doc(`The conversation as of the last committed step: immutable, branchable, persistable.`,
				`The seed conversation if the agent never stepped.`,
				`Branching from it does not affect the agent.`),

		dagql.NodeFunc("start", s.start).
			DoNotCache("Imperatively mutates runtime state.").
			Doc(`Start the agent's evaluation loop. No-op if it is already running.`,
				`The loop runs detached from the calling request: it steps the conversation while input is pending, then idles awaiting further lifecycle operations.`),

		dagql.NodeFunc("waitFor", s.waitFor).
			DoNotCache("Blocks on live runtime state.").
			Doc(`Block until the agent reaches the given state, returning immediately if it is already there.`,
				`Fails if the state becomes unreachable, e.g. waiting for RUNNING on a stopped agent.`).
			Args(
				dagql.Arg("state").Doc(`The lifecycle state to wait for.`),
			),

		dagql.NodeFunc("stop", s.stop).
			DoNotCache("Imperatively mutates runtime state.").
			Doc(`Release the agent's runtime. The tombstone (state, snapshot) stays readable for the rest of the session.`).
			Args(
				dagql.Arg("kill").Doc(`Cancel the loop immediately instead of letting an in-flight step finish. Either way the completed steps are preserved in the snapshot.`),
			),
	}.Install(srv)

	core.AgentStates.Install(srv)
}

func (s agentSchema) name(ctx context.Context, agent *core.Agent, _ struct{}) (string, error) {
	return agent.Name, nil
}

func agentRuntimes(ctx context.Context) (*core.AgentRuntimes, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	return query.Agents(ctx)
}

func (s agentSchema) state(ctx context.Context, parent dagql.ObjectResult[*core.Agent], _ struct{}) (core.AgentState, error) {
	agents, err := agentRuntimes(ctx)
	if err != nil {
		return "", err
	}
	rt, found, err := agents.Get(ctx, parent)
	if err != nil {
		return "", err
	}
	if !found {
		// Never started: no runtime entry exists (reads never create one),
		// and the projected state of that absence is IDLE — mailbox empty,
		// no turn open. The entry is created lazily by start/waitFor/stop.
		return core.AgentStateIdle, nil
	}
	return rt.State(), nil
}

func (s agentSchema) snapshot(ctx context.Context, parent dagql.ObjectResult[*core.Agent], _ struct{}) (res dagql.ObjectResult[*core.LLM], _ error) {
	agents, err := agentRuntimes(ctx)
	if err != nil {
		return res, err
	}
	rt, found, err := agents.Get(ctx, parent)
	if err != nil {
		return res, err
	}
	if !found {
		// Never started: the last committed conversation is the seed.
		return parent.Self().Seed, nil
	}
	return rt.Snapshot(), nil
}

func (s agentSchema) start(ctx context.Context, parent dagql.ObjectResult[*core.Agent], _ struct{}) (dagql.ObjectResult[*core.Agent], error) {
	agents, err := agentRuntimes(ctx)
	if err != nil {
		return parent, err
	}
	if _, err := agents.Start(ctx, parent); err != nil {
		return parent, err
	}
	return parent, nil
}

func (s agentSchema) waitFor(ctx context.Context, parent dagql.ObjectResult[*core.Agent], args struct {
	State core.AgentState `default:"IDLE"`
}) (dagql.ObjectResult[*core.Agent], error) {
	agents, err := agentRuntimes(ctx)
	if err != nil {
		return parent, err
	}
	// Create the entry lazily (without starting the loop) so waiting on a
	// never-started agent blocks on future transitions instead of erroring.
	rt, err := agents.GetOrCreate(ctx, parent)
	if err != nil {
		return parent, err
	}
	if err := rt.WaitFor(ctx, args.State); err != nil {
		return parent, err
	}
	return parent, nil
}

func (s agentSchema) stop(ctx context.Context, parent dagql.ObjectResult[*core.Agent], args struct {
	Kill bool `default:"false"`
}) (dagql.ObjectResult[*core.Agent], error) {
	agents, err := agentRuntimes(ctx)
	if err != nil {
		return parent, err
	}
	// Stopping a never-started agent still creates its entry, transitioned
	// straight to the tombstone: stop is terminal either way, and idempotent.
	rt, err := agents.GetOrCreate(ctx, parent)
	if err != nil {
		return parent, err
	}
	if err := rt.Stop(ctx, args.Kill, nil); err != nil {
		return parent, err
	}
	return parent, nil
}
