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

// TestAgentReseedRetriesPendingInput covers branching from a prompt: the
// adopted conversation ends in input the model has not answered. On a live,
// unpaused loop — parked in receive, which only a wake moves — reseed must open
// the turn and wake the loop, or the projection claims RUNNING forever while
// nothing steps. Anywhere the loop is not parked live (paused, never started)
// the pending input waits for resume or start, and a settled conversation
// wakes nothing.
func TestAgentReseedRetriesPendingInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*LLM](srv))
	conversation := func(name string, msgs ...*LLMMessage) dagql.ObjectResult[*LLM] {
		llm, err := dagql.NewObjectResultForCall(&LLM{Messages: msgs}, srv, &dagql.ResultCall{
			Kind: dagql.ResultCallKindSynthetic, SyntheticOp: name,
			Type: dagql.NewResultCallType((&LLM{}).Type()),
		})
		require.NoError(t, err)
		return llm
	}
	prompt := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "retry me"}}}
	reply := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "done"}}}
	newRuntime := func(started, paused bool) *AgentRuntime {
		return &AgentRuntime{
			name:         "branched",
			messages:     map[string]*agentMessageRecord{},
			stateChanged: make(chan struct{}),
			wake:         make(chan struct{}, 1),
			started:      started,
			paused:       paused,
			last:         conversation("tip", prompt, reply),
		}
	}
	woken := func(rt *AgentRuntime) bool {
		select {
		case <-rt.wake:
			return true
		default:
			return false
		}
	}

	t.Run("idle loop steps the pending prompt", func(t *testing.T) {
		rt := newRuntime(true, false)
		require.Equal(t, AgentStateIdle, rt.State())
		pending := conversation("pending", prompt)
		require.NoError(t, rt.Reseed(ctx, pending))
		require.Same(t, pending.Self(), rt.Snapshot().Self())
		require.True(t, woken(rt), "the parked loop must be woken to step the retried prompt")
		require.True(t, rt.turnOpen, "the retried input opens a turn")
		require.Equal(t, AgentStateRunning, rt.State())

		// A send racing the wake joins the retried turn rather than
		// claiming a new one.
		ref, err := rt.enqueue("follow-up", nil, "")
		require.NoError(t, err)
		require.Equal(t, AgentMessageSteered, rt.messages[ref].deliveryHint)
	})

	t.Run("settled conversation stays idle", func(t *testing.T) {
		rt := newRuntime(true, false)
		require.NoError(t, rt.Reseed(ctx, conversation("settled", prompt, reply)))
		require.False(t, woken(rt))
		require.False(t, rt.turnOpen)
		require.Equal(t, AgentStateIdle, rt.State())
	})

	t.Run("paused loop waits for resume", func(t *testing.T) {
		rt := newRuntime(true, true)
		require.NoError(t, rt.Reseed(ctx, conversation("pending", prompt)))
		require.False(t, woken(rt), "pause takes priority over pending work")
		require.False(t, rt.turnOpen)
		require.Equal(t, AgentStatePaused, rt.State())
	})

	t.Run("never-started entry waits for start", func(t *testing.T) {
		rt := newRuntime(false, false)
		require.NoError(t, rt.Reseed(ctx, conversation("pending", prompt)))
		require.False(t, woken(rt))
		require.False(t, rt.turnOpen)
		require.Equal(t, AgentStateIdle, rt.State())
	})
}
