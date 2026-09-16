package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestAgentReseedRejectsPausedDrain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt := &AgentRuntime{
		name:         "drainer",
		messages:     map[string]*agentMessageRecord{},
		stateChanged: make(chan struct{}),
		wake:         make(chan struct{}, 1),
	}
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*LLM](srv))
	conversation := func(name string) dagql.ObjectResult[*LLM] {
		llm, err := dagql.NewObjectResultForCall(&LLM{}, srv, &dagql.ResultCall{
			Kind: dagql.ResultCallKindSynthetic, SyntheticOp: name,
			Type: dagql.NewResultCallType((&LLM{}).Type()),
		})
		require.NoError(t, err)
		return llm
	}
	old, replacement := conversation("old"), conversation("replacement")
	rt.last = old
	ref, err := rt.enqueue("queued prompt", nil, "")
	require.NoError(t, err)

	// Reproduce the state while drainMailbox is recording a popped prompt
	// outside the mutex. An ordinary pause does not invalidate that result.
	rt.mailbox = nil
	rt.draining = true
	require.NoError(t, rt.Pause())
	require.Equal(t, AgentStatePaused, rt.State())
	require.ErrorContains(t, rt.Reseed(ctx, replacement), "draining")
	require.Same(t, old.Self(), rt.Snapshot().Self())
	require.False(t, rt.messages[ref].resolved)

	// Once the drain commits and parks, reseed may abandon its suspended
	// turn and settle the consumed message as a rewind.
	rt.draining = false
	rt.turnOpen = true
	rt.messages[ref].consumed = true
	rt.consumed = append(rt.consumed, rt.messages[ref])
	require.NoError(t, rt.Reseed(ctx, replacement))
	require.Same(t, replacement.Self(), rt.Snapshot().Self())
	require.True(t, rt.messages[ref].resolved)
	require.ErrorContains(t, rt.messages[ref].err, "rewound")
	require.Equal(t, AgentStatePaused, rt.State())
}
