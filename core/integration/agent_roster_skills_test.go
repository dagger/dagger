package core

// This file extends the roster-addressing coverage in agent_runtime_test.go
// to seeds that carry a SKILLS DIRECTORY — LLM.withSkills(directory:), which
// LLM.recipeSelectors re-emits into the spawned agent's pinned chain as an
// ID-literal argument.
//
// An ID-literal argument is the one place a chain reaches sideways instead of
// down the receiver spine, and the payload published for a call flattens it:
// callpbv1 carries an embedded ID as a bare digest reference (call.LiteralID),
// so the argument's OWN frames are not in the referring span's payload. A
// client rebuilding the chain has to find each of them by digest among the
// payloads it ingested, which means they are only resolvable if the calls that
// built the directory got client-visible spans of their own.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// skillDoc is the SKILL.md every case installs. It mirrors SkillDoc in the
// agent-skiller fixture (core/integration/testdata/modules/go/agent-skiller),
// so all three cases install byte-identical content and differ only in WHO
// built the directory.
const skillDoc = `---
name: greeter
description: Greet somebody, from inside a module.
---

Say hello.
`

// spawnAgentWithSkills spawns one agent from a seed carrying a skills
// directory, and returns its handle. Raw GraphQL, like the other roster
// tests: the point is the exact chain the agent's ID records.
func spawnAgentWithSkills(ctx context.Context, t *testctx.T, c *dagger.Client, name string, dirID dagger.ID) *agentHandle {
	t.Helper()
	res := map[string]any{}
	require.NoError(t, c.Do(ctx,
		&dagger.Request{
			Query: `query($model: String!, $dir: ID!, $name: String!) {
				llm(model: $model) {
					withSkills(directory: $dir) { spawn(name: $name) }
				}
			}`,
			Variables: map[string]any{
				"model": emptyReplayModel,
				"dir":   string(dirID),
				"name":  name,
			},
		},
		&dagger.Response{Data: &res},
	))
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	out := gjson.Get(string(raw), "llm.withSkills.spawn")
	require.True(t, out.Exists() && out.String() != "",
		"spawned agent ID missing in response: %s", raw)
	return &agentHandle{c: c, agentID: out.String()}
}

// TestRosterAddressingWithSkills is TestRosterAddressing with a skills
// directory bound into the seed: the shape that broke in the wild, where a
// TUI could not focus an agent on its own roster —
//
//	agent "scout" cannot be addressed: cannot rebuild ID for "agent" (Agent):
//	call xxh3:47ab… never reached this client, referenced as argument
//	"directory" of "withSkills" (LLM) xxh3:edf3…(directory: xxh3:47ab…)
//
// The three cases differ ONLY in who built the directory, which is what
// decides whether its frames ever got a span this client ingested:
//
//   - client directory — the client builds it in its own session, so every
//     frame is a call this client made and watched. Rebuilds today.
//   - module directory — built inside a module function, i.e. by the module's
//     nested session. Rebuilds today too: nested-session spans are forwarded
//     to the client, and the chain that comes back is the module's OWN
//     internal directory.withNewFile(…), not the module function call — the
//     same property TestRosterAddressingFromModule leans on.
//   - directory from another session — built by a second CLI session, whose
//     telemetry this client never sees. Loading an ID never re-selects the
//     calls behind it (the engine resolves it straight out of its cache), so
//     no span for those frames is ever emitted HERE: the seed composes, the
//     skills are readable, the agent spawns and runs — and the frames the
//     chain needs are simply not in this client's DB.
//
// The assertion is the same in all three: a roster entry is worth nothing if
// the client cannot turn it back into a WORKING handle on that same live
// runtime. The identity assertions past the rebuild are ones a freshly
// derived agent value could never satisfy — a FAILED runtime, a transcript
// holding a marker only this instance ever received, and a send that reports
// QUEUED where an inert agent would report STARTED.
func (AgentRuntimeSuite) TestRosterAddressingWithSkills(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		// An inherited session is already attached to somebody else's
		// frontend; only a CLI session this test starts can be pointed at
		// the sink.
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}

	for _, tc := range []struct {
		name string
		// skills returns the ID of the skills directory to bind into the
		// seed. c is the sink-connected client.
		skills func(ctx context.Context, t *testctx.T, c *dagger.Client) dagger.ID
	}{
		{
			name: "client directory",
			skills: func(ctx context.Context, t *testctx.T, c *dagger.Client) dagger.ID {
				return newSkillsDir(ctx, t, c)
			},
		},
		{
			name: "module directory",
			skills: func(ctx context.Context, t *testctx.T, c *dagger.Client) dagger.ID {
				modDir := t.TempDir()
				copyTestdataFixture(ctx, t, modDir, "modules", "go", "agent-skiller")
				require.NoError(t, c.ModuleSource(modDir).AsModule().Serve(ctx))
				return queryID(ctx, t, c, `{ skiller { skills { id } } }`, "skiller.skills.id")
			},
		},
		{
			name: "directory from another session",
			skills: func(ctx context.Context, t *testctx.T, c *dagger.Client) dagger.ID {
				// A second CLI session, whose telemetry goes nowhere near
				// this test's sink. It stays open for the duration of the
				// test, so the value it built stays addressable.
				other := connect(ctx, t)
				return newSkillsDir(ctx, t, other)
			},
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			sink := newAgentTraceSink(t)
			c := connect(ctx, t, sink.clientOpts()...)

			dirID := tc.skills(ctx, t, c)
			// The directory really is a skills directory, so a case cannot
			// pass (or fail) vacuously on a mis-built fixture.
			requireSkillInstalled(ctx, t, c, dirID, "greeter")

			h := spawnAgentWithSkills(ctx, t, c, "rostered", dirID)

			// A per-run marker, so "the reconstructed handle sees this
			// runtime" cannot pass by coincidence.
			marker := "roster marker " + identity.NewID()
			delivery, err := h.sendNoWait(ctx, t, marker)
			require.NoError(t, err)
			require.Equal(t, "STARTED", delivery)

			// The empty recording fails the first model call, so the loop
			// lands in FAILED — a state no never-started agent can project,
			// and one that also makes further sends QUEUED rather than
			// STARTED.
			state, err := h.waitFor(ctx, t, "FAILED")
			require.NoError(t, err)
			require.Equal(t, "FAILED", state)

			node := sink.awaitAgent(t, "FAILED")
			require.Equal(t, "rostered", node.Name)

			// The whole chain rebuilds — including the frames behind the
			// withSkills argument, which the published payload carries only
			// as a digest reference.
			rebuilt, rebuiltID := sink.rebuild(t, c, node)
			display := rebuiltID.Display()
			t.Logf("rebuilt chain: %s", display)
			require.Contains(t, display, "withSkills(",
				"the skills binding must be in the rebuilt chain")
			require.Contains(t, display,
				fmt.Sprintf(`agent(id: %q, name: %q)`, node.ID, "rostered"))

			// Everything past here comes from the runtime registry rather
			// than the recipe, which is what addressing has to reach.
			require.Equal(t, "rostered",
				rebuilt.mustRun(ctx, t, `name`).Get("name").String())
			require.Equal(t, node.ID,
				rebuilt.mustRun(ctx, t, `instanceID`).Get("instanceID").String())
			require.Equal(t, "FAILED", rebuilt.state(ctx, t))
			transcript, _ := rebuilt.snapshot(ctx, t)
			require.Contains(t, transcript, marker)

			// And it is sendable, which is the whole point: mail lands in
			// the live mailbox, queued behind the tombstone a resume would
			// drain. An inert agent would have reported STARTED.
			delivery, err = rebuilt.sendNoWait(ctx, t, "sent to an agent this client never spawned")
			require.NoError(t, err)
			require.Equal(t, "QUEUED", delivery)
		})
	}
}

// newSkillsDir builds the skills directory through the given client and
// returns its ID.
func newSkillsDir(ctx context.Context, t *testctx.T, c *dagger.Client) dagger.ID {
	t.Helper()
	res := map[string]any{}
	require.NoError(t, c.Do(ctx,
		&dagger.Request{
			Query: `query($contents: String!) {
				directory {
					withNewFile(path: "greeter/SKILL.md", contents: $contents) { id }
				}
			}`,
			Variables: map[string]any{"contents": skillDoc},
		},
		&dagger.Response{Data: &res},
	))
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	out := gjson.Get(string(raw), "directory.withNewFile.id")
	require.True(t, out.Exists() && out.String() != "",
		"skills directory ID missing in response: %s", raw)
	return dagger.ID(out.String())
}

// requireSkillInstalled checks that binding the directory really does install
// the skill, so a case cannot pass or fail on a directory that was never a
// skills directory in the first place.
func requireSkillInstalled(ctx context.Context, t *testctx.T, c *dagger.Client, dirID dagger.ID, skill string) {
	t.Helper()
	res := map[string]any{}
	require.NoError(t, c.Do(ctx,
		&dagger.Request{
			Query: `query($model: String!, $dir: ID!) {
				llm(model: $model) { withSkills(directory: $dir) { skills { name } } }
			}`,
			Variables: map[string]any{"model": emptyReplayModel, "dir": string(dirID)},
		},
		&dagger.Response{Data: &res},
	))
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	var names []string
	for _, s := range gjson.Get(string(raw), "llm.withSkills.skills").Array() {
		names = append(names, s.Get("name").String())
	}
	require.Contains(t, names, skill, "the bound directory must expose the %q skill", skill)
}
