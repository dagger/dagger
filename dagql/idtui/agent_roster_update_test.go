package idtui

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

// updateAgentRoster runs on every span and log batch, so it skips the rebuild
// while nothing the roster is built from has changed. These tests hold it to
// both halves of that: every input still reaches the strip, and an unchanged
// roster -- however large -- costs nothing per batch.

// renderedRosterEntry is the entry the strip last rendered for an agent.
func renderedRosterEntry(t *testing.T, fe *frontendPretty, id string) AgentRosterEntry {
	t.Helper()
	for _, entry := range fe.agentRosterState {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("no agent %q on the rendered roster", id)
	return AgentRosterEntry{}
}

// TestRosterUpdateFollowsEveryInput: a state change, a rename, a focus move
// and a read-only mark each reach the strip, however the skip is keyed.
func TestRosterUpdateFollowsEveryInput(t *testing.T) {
	handler := &focusShellHandler{target: "agent-chief"}
	db := rosterDB(t)
	fe := focusTestFrontend(t, db, handler)
	fe.updateAgentRoster()

	require.Len(t, fe.agentRosterState, 2)
	require.True(t, renderedRosterEntry(t, fe, "agent-chief").Focused)
	require.False(t, renderedRosterEntry(t, fe, "agent-scout").Focused)

	// State rides control records on the log stream.
	publishAgentControl(t, db, "agent-scout", func(a *agentcontrol.Agent) { a.State = "RUNNING" })
	fe.updateAgentRoster()
	require.Equal(t, "RUNNING", renderedRosterEntry(t, fe, "agent-scout").State)
	publishAgentControl(t, db, "agent-scout", func(a *agentcontrol.Agent) { a.State = "IDLE" })
	fe.updateAgentRoster()
	require.Equal(t, "IDLE", renderedRosterEntry(t, fe, "agent-scout").State)

	// So does the display name, and the strip repaints with it.
	publishAgentControl(t, db, "agent-scout", func(a *agentcontrol.Agent) { a.Name = "lookout" })
	fe.updateAgentRoster()
	require.Equal(t, "lookout", renderedRosterEntry(t, fe, "agent-scout").Name)
	require.Contains(t, strings.Join(fe.tui.Step(), "\n"), "2 lookout")

	// Focus is the client's belief, which moves before the handler does.
	fe.pendingFocusAgent = "agent-scout"
	fe.updateAgentRoster()
	require.True(t, renderedRosterEntry(t, fe, "agent-scout").Focused)
	require.False(t, renderedRosterEntry(t, fe, "agent-chief").Focused)
	fe.pendingFocusAgent = ""
	fe.updateAgentRoster()
	require.True(t, renderedRosterEntry(t, fe, "agent-chief").Focused)

	// Read-only marks live on the frontend, not in the trace.
	fe.markAgentUnaddressable("agent-scout")
	fe.updateAgentRoster()
	require.True(t, renderedRosterEntry(t, fe, "agent-scout").ReadOnly)
	fe.clearAgentUnaddressable("agent-scout")
	fe.updateAgentRoster()
	require.False(t, renderedRosterEntry(t, fe, "agent-scout").ReadOnly)
}

// largeRosterDB is a trace publishing n agents, each with a loop span and a
// control record carrying a snapshot digest, ingested in one batch apiece.
func largeRosterDB(t testing.TB, n int) *dagui.DB {
	t.Helper()
	db := dagui.NewDB()
	start := time.Unix(100, 0)
	traceID := prettyTestTraceID()
	snapshots := make([]dagui.SpanSnapshot, 0, n)
	records := make([]sdklog.Record, 0, n)
	for i := range n {
		var spanID trace.SpanID
		binary.BigEndian.PutUint64(spanID[:], uint64(i+1))
		handle := fmt.Sprintf("agent-%d", i)
		name := fmt.Sprintf("worker-%d", i)
		digest := "sha256:" + name
		snapshots = append(snapshots, dagui.SpanSnapshot{
			ID:              dagui.SpanID{SpanID: spanID},
			TraceID:         traceID,
			Name:            "agent: " + name,
			StartTime:       start.Add(time.Duration(i) * time.Millisecond),
			Agent:           true,
			AgentID:         handle,
			AgentName:       name,
			AgentCallDigest: digest,
		})
		a := agentcontrol.Agent{
			Key: agentcontrol.Key{
				Namespace: agentcontrol.Namespace{Session: "session", Trace: traceID.String(), Incarnation: "incarnation"},
				Handle:    handle,
			},
			Revision:   1,
			Activity:   time.Unix(1, 0).UTC(),
			Name:       name,
			CallDigest: digest,
			State:      "RUNNING",
			Digest:     "xxh3:" + handle + "-step",
		}
		rec := a.Record()
		var attrs []otellog.KeyValue
		rec.WalkAttributes(func(kv otellog.KeyValue) bool {
			attrs = append(attrs, kv)
			return true
		})
		records = append(records, frontendTestLogRecord(trace.SpanID{}, otellog.StringValue(""), attrs...))
	}
	db.ImportSnapshots(snapshots)
	db.IngestLogs(records)
	_, _, err := db.AgentControl()
	require.NoError(t, err, "fixture: control record rejected")
	require.Len(t, db.Agents(), n)
	return db
}

// TestRosterUpdateUnchangedDoesNotAllocate is the regression this skip
// exists for: with thousands of agents, rebuilding and fingerprinting the
// roster on every batch kept the UI loop busy and fed the GC gigabytes a
// second. An unchanged roster must cost no allocation at all.
func TestRosterUpdateUnchangedDoesNotAllocate(t *testing.T) {
	const agents = 5000
	handler := &stepShellHandler{}
	handler.target = "agent-0"
	fe := stepTestFrontend(t, largeRosterDB(t, agents), handler)
	fe.updateAgentRoster()
	fe.notifyAgentSteps()
	require.Len(t, fe.agentRosterState, agents)
	require.True(t, fe.agentRosterState[0].Focused)
	require.Len(t, handler.stepped(), agents, "sanity: every agent's first step is notified")

	allocs := testing.AllocsPerRun(100, func() {
		fe.updateAgentRoster()
		fe.notifyAgentSteps()
	})
	require.Zero(t, allocs)
	require.Len(t, handler.stepped(), agents, "an unchanged roster holds no new step")
}

func BenchmarkUpdateAgentRosterUnchanged(b *testing.B) {
	b.Setenv("NO_COLOR", "1")
	handler := &stepShellHandler{}
	handler.target = "agent-0"
	db := largeRosterDB(b, 5000)
	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 10))
	fe.setupTUI()
	fe.startShell(context.Background(), handler)
	fe.updateAgentRoster()
	fe.notifyAgentSteps()
	b.ReportAllocs()
	for b.Loop() {
		fe.updateAgentRoster()
		fe.notifyAgentSteps()
	}
}
