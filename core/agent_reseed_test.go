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

// TestAgentReseedHoldsPendingInput covers a reseed whose conversation ends in
// input the model has not answered: a compaction summary, a branch summary, or
// a branched-from prompt. Reseed itself never steps it — the input is HELD to
// lead the next turn, and the projection says so (IDLE, not a parked loop's
// false RUNNING). Retrying it is the caller's explicit choice: Resume steps
// held input, opening the turn truthfully; a message sent instead opens a
// fresh turn with the held input in front of it.
func TestAgentReseedHoldsPendingInput(t *testing.T) {
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
	prompt := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "summary or prompt"}}}
	reply := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "done"}}}
	newRuntime := func(started, paused bool) *AgentRuntime {
		return &AgentRuntime{
			name:         "reseeded",
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

	t.Run("idle loop holds the input", func(t *testing.T) {
		rt := newRuntime(true, false)
		pending := conversation("pending", prompt)
		require.NoError(t, rt.Reseed(ctx, pending))
		require.Same(t, pending.Self(), rt.Snapshot().Self())
		require.False(t, woken(rt), "a compaction or branch summary must not be answered on its own")
		require.True(t, rt.held)
		require.False(t, rt.turnOpen)
		require.Equal(t, AgentStateIdle, rt.State(), "held input is not a turn in flight")

		// The next message opens a fresh turn, the held input in front of
		// it.
		ref, err := rt.enqueue("next prompt", nil, "")
		require.NoError(t, err)
		require.Equal(t, AgentMessageStarted, rt.messages[ref].deliveryHint)
		require.True(t, woken(rt))
	})

	t.Run("resume retries the held input", func(t *testing.T) {
		rt := newRuntime(true, false)
		require.NoError(t, rt.Reseed(ctx, conversation("pending", prompt)))
		require.NoError(t, rt.Resume(ctx))
		require.True(t, woken(rt), "resume wakes the parked loop to step it")
		require.False(t, rt.held)
		require.True(t, rt.turnOpen, "the retried input opens a turn")
		require.Equal(t, AgentStateRunning, rt.State())

		// A send racing the wake joins the retried turn rather than
		// claiming a new one.
		ref, err := rt.enqueue("follow-up", nil, "")
		require.NoError(t, err)
		require.Equal(t, AgentMessageSteered, rt.messages[ref].deliveryHint)
	})

	t.Run("resume of an interrupted agent retries too", func(t *testing.T) {
		rt := newRuntime(true, true)
		require.NoError(t, rt.Reseed(ctx, conversation("pending", prompt)))
		require.False(t, woken(rt))
		require.Equal(t, AgentStatePaused, rt.State())
		require.NoError(t, rt.Resume(ctx))
		require.True(t, woken(rt))
		require.Equal(t, AgentStateRunning, rt.State())
	})

	t.Run("settled conversation holds nothing", func(t *testing.T) {
		rt := newRuntime(true, false)
		require.NoError(t, rt.Reseed(ctx, conversation("settled", prompt, reply)))
		require.False(t, rt.held)
		require.Equal(t, AgentStateIdle, rt.State())
		// Resume on an idle agent with nothing held stays a no-op.
		require.NoError(t, rt.Resume(ctx))
		require.False(t, rt.turnOpen)
		require.Equal(t, AgentStateIdle, rt.State())
	})

	t.Run("a later commit releases the hold", func(t *testing.T) {
		rt := newRuntime(true, false)
		require.NoError(t, rt.Reseed(ctx, conversation("pending", prompt)))
		require.True(t, rt.held)
		// The drain recording the next message commits a conversation the
		// loop steps: the held input is now part of that turn.
		rt.mu.Lock()
		rt.transitionLocked(func() {
			rt.commitLast(ctx, conversation("with message", prompt, prompt))
			rt.turnOpen = true
		})
		rt.mu.Unlock()
		require.False(t, rt.held)
		require.Equal(t, AgentStateRunning, rt.State())
	})
}
