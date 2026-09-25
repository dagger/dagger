package dagui

import (
	"testing"
	"time"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

func controlRecord(rec log.Record) sdklog.Record {
	var attrs []log.KeyValue
	rec.WalkAttributes(func(kv log.KeyValue) bool { attrs = append(attrs, kv); return true })
	return newTestLogRecord(trace.TraceID{1}, trace.SpanID{}, "", attrs...)
}

func TestCanonicalDormantRosterAndHistoryIsolation(t *testing.T) {
	db := NewDB()
	live := trace.TraceID{1}
	root := agentTestSpan(1, "live", SpanID{})
	root.TraceID = TraceID{TraceID: live}
	db.ImportSnapshots([]SpanSnapshot{root})
	a := agentcontrol.Agent{Key: agentcontrol.Key{Namespace: agentcontrol.Namespace{Session: "source", Trace: trace.TraceID{2}.String(), Incarnation: "source"}, Handle: "worker"},
		Revision: 10, Name: "worker", Parent: "chief", State: "FAILED", Failure: "original failure", Digest: "xxh3:source", Activity: time.Unix(10, 0).UTC()}
	db.ingestLogs([]sdklog.Record{controlRecord(a.Record())}, true)
	nodes := db.Agents()
	require.Len(t, nodes, 1, "dormant creation needs no loop span")
	require.Equal(t, "FAILED", nodes[0].State)
	plan := db.RestorePlan()
	require.Len(t, plan, 1)
	require.Equal(t, "chief", plan[0].ParentAgentID)
	require.Equal(t, "original failure", plan[0].Error)
	require.Equal(t, a.Key, plan[0].Source)
	current := a
	current.Session, current.Trace, current.Incarnation = "destination", live.String(), "destination"
	current.Revision, current.State, current.Failure, current.Digest = 1, "PAUSED", "", "xxh3:current"
	// Deliberately older producer activity: clock skew cannot allow a source
	// namespace to win over the live destination incarnation.
	current.Activity = time.Unix(1, 0).UTC()
	db.ingestLogs([]sdklog.Record{controlRecord(current.Record())}, true)
	a.Revision, a.State, a.Failure = 11, "RUNNING", ""
	db.ingestLogs([]sdklog.Record{controlRecord(a.Record())}, true)
	legacy := agentLoopSnapshot(2, "worker", "obsolete name", SpanID{})
	legacy.TraceID = TraceID{TraceID: trace.TraceID{2}}
	db.ImportSnapshots([]SpanSnapshot{legacy})
	db.ingestLogs([]sdklog.Record{newTestAgentStateRecord(legacy.ID, "RUNNING", "", ""), newTestAgentSnapshotRecord(legacy.ID, "xxh3:obsolete")}, true)
	nodes = db.Agents()
	require.Equal(t, "PAUSED", nodes[0].State)
	require.Equal(t, "xxh3:current", nodes[0].SnapshotDigest)
	require.False(t, nodes[0].Live(), "historical running span does not activate restored agent")
	require.Empty(t, db.RestorePlan(), "destination already holds the runtime")
	projections, _, err := db.AgentControl()
	require.NoError(t, err)
	require.Len(t, projections, 2, "source and destination revisions remain independently indexed")
}

func TestCanonicalCaptureFailuresStayOutOfOutput(t *testing.T) {
	db := NewDB()
	a := agentcontrol.Agent{Key: agentcontrol.Key{Namespace: agentcontrol.Namespace{Session: "session", Trace: "trace", Incarnation: "runtime"}, Handle: "local"}, Name: "local", CaptureError: "agent capture depends on originating client via Host.__gitDir"}
	for revision := int64(1); revision <= 20; revision++ {
		a.Revision = revision
		a.State = "IDLE"
		if revision%2 == 0 {
			a.State = "RUNNING"
		}
		renderable := db.ingestLogs([]sdklog.Record{controlRecord(a.Record())}, true)
		require.Empty(t, renderable, "capture availability is control state, not a repeated warning")
		nodes := db.Agents()
		require.Len(t, nodes, 1)
		require.Equal(t, a.State, nodes[0].State, "capture failure must not replace normal runtime state")
		require.Empty(t, nodes[0].Control.Failure, "capture failure is not a model-loop failure")
	}
	_, _, err := db.AgentControl()
	require.NoError(t, err, "an unavailable capture is valid telemetry")
	_, err = a.RestoreState()
	require.ErrorContains(t, err, "Host.__gitDir", "an explicit restore still explains why it cannot proceed")
}

func TestCanonicalRemovalAndMalformedRecords(t *testing.T) {
	db := NewDB()
	a := agentcontrol.Agent{Key: agentcontrol.Key{Namespace: agentcontrol.Namespace{Session: "session", Trace: "trace", Incarnation: "generation"}, Handle: "a"}, Revision: 1, State: "IDLE", Digest: "xxh3:anchor"}
	db.ingestLogs([]sdklog.Record{controlRecord(a.Record())}, true)
	require.Len(t, db.Agents(), 1)
	a.Revision, a.Removed, a.State, a.StopReason = 2, true, "STOPPED", "EXPLICIT"
	db.ingestLogs([]sdklog.Record{controlRecord(a.Record())}, true)
	require.Empty(t, db.Agents())
	all, _, err := db.AgentControl()
	require.NoError(t, err)
	require.Len(t, all, 1, "removal evidence remains available to bootstrap consumers")
	bad := a.Record()
	bad.AddAttributes(log.Int(agentcontrol.VersionAttr, 99))
	renderable := db.ingestLogs([]sdklog.Record{controlRecord(bad)}, true)
	require.Empty(t, renderable)
	_, _, err = db.AgentControl()
	require.ErrorContains(t, err, "unsupported agent control version")
}
