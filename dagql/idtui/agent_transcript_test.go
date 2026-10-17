package idtui

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// The exported roster and transcript renderers serve the engine's ReadTrace
// builtin: same rendering as the console, with its own next-step hints.
func TestTraceTranscriptHints(t *testing.T) {
	roster := []TraceAgent{
		{ID: "chief-id", Name: "chief", State: "IDLE", SnapshotDigest: "xxh3:chief", SpanIDs: []string{"01", "02"}},
		{ID: "worker-id", Name: "worker", ParentID: "chief-id", SpanIDs: []string{"03"}},
		{ID: "orphan-id", Name: "orphan", ParentID: "gone-id", SpanIDs: []string{"04"}, SnapshotDigest: "xxh3:orphan"},
	}
	got := RenderTraceAgents(roster, AgentRosterOpts{
		Recorded: true, All: true, Header: "header", ShowSpans: true,
		ReadHint: `ReadTrace(view: "transcript", agent: <handle>)`, CheckpointNote: "inferred",
	})
	require.Contains(t, got, `3 agent(s); read one with ReadTrace(view: "transcript", agent: <handle>).`)
	require.Contains(t, got, "chief-id  chief  IDLE  span=02\n  worker-id  worker  -  span=03  (inferred)\n")
	require.Contains(t, got, "orphan-id  orphan  -  span=04  (parent agent gone-id)\n")
	require.NotContains(t, got, "transcript(agent: <handle or unique name>)")

	var snap TraceTranscript
	snap.Source = "recorded-checkpoint"
	for i := range 3 {
		snap.Snapshot.Messages = append(snap.Snapshot.Messages, TraceTranscriptMessage{
			Role: "USER", Content: []consoleTranscriptBlock{{Kind: "TEXT", Text: fmt.Sprintf("prompt %d", i)}},
		})
	}
	messages := SelectTraceTranscript(snap.Snapshot.Messages, "user", "", nil)
	page := RenderTraceTranscript(TraceTranscriptPage{
		Agent: roster[0], Transcript: snap, Messages: messages, Limit: 1, Clip: TraceTranscriptDefaultClip,
		Filters:  "all",
		NextPage: func(offset int) string { return fmt.Sprintf("ReadTrace(agent: %q, offset: %d)", "chief-id", offset) },
	})
	require.Contains(t, page, `next page: ReadTrace(agent: "chief-id", offset: 1) (2 more)`)
	require.NotContains(t, page, "offset=1")
	// Without a NextPage hint, the console's own form.
	page = RenderTraceTranscript(TraceTranscriptPage{Agent: roster[0], Transcript: snap, Messages: messages, Limit: 1})
	require.Contains(t, page, "next page: offset=1 (2 more)")
}
