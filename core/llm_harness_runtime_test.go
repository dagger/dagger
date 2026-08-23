package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLLMHarnessAdapter struct {
	mu sync.Mutex

	events chan LLMHarnessEvent
	starts []LLMHarnessInput
	steers []LLMHarnessInput
	turns  []string

	steerErr       error
	startTurnHook  func(LLMHarnessInput)
	interruptCalls []string
	startCalls     int
	closeCalls     int
	native         LLMHarnessNativeState
}

func newFakeLLMHarnessAdapter() *fakeLLMHarnessAdapter {
	return &fakeLLMHarnessAdapter{
		events: make(chan LLMHarnessEvent, 32),
		native: LLMHarnessNativeState{NativeSession: "session", Protocol: "fake-v1"},
	}
}

func (adapter *fakeLLMHarnessAdapter) Start(context.Context, LLMHarnessStart) (LLMHarnessSession, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.startCalls++
	return LLMHarnessSession{NativeSession: adapter.native.NativeSession, Protocol: adapter.native.Protocol}, nil
}

func (adapter *fakeLLMHarnessAdapter) StartTurn(_ context.Context, input LLMHarnessInput) error {
	adapter.mu.Lock()
	adapter.starts = append(adapter.starts, input)
	hook := adapter.startTurnHook
	adapter.mu.Unlock()
	if hook != nil {
		hook(input)
	}
	return nil
}

func (adapter *fakeLLMHarnessAdapter) Steer(_ context.Context, turn string, input LLMHarnessInput) error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.turns = append(adapter.turns, turn)
	adapter.steers = append(adapter.steers, input)
	err := adapter.steerErr
	adapter.steerErr = nil
	return err
}

func (adapter *fakeLLMHarnessAdapter) Interrupt(_ context.Context, turn string, _ bool) error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.interruptCalls = append(adapter.interruptCalls, turn)
	return nil
}

func (*fakeLLMHarnessAdapter) CancelQueued(context.Context, string) error { return nil }
func (adapter *fakeLLMHarnessAdapter) Events() <-chan LLMHarnessEvent     { return adapter.events }
func (adapter *fakeLLMHarnessAdapter) Quiesce(context.Context) (LLMHarnessNativeState, error) {
	return adapter.native, nil
}
func (adapter *fakeLLMHarnessAdapter) Close(context.Context) error {
	adapter.mu.Lock()
	adapter.closeCalls++
	adapter.mu.Unlock()
	return nil
}

func harnessPrompt(text string) []*LLMMessage {
	return []*LLMMessage{{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: text}}}}
}

func harnessLifecycle(id, turn string, state LLMHarnessMessageState) LLMHarnessMessageLifecycle {
	return LLMHarnessMessageLifecycle{DaggerMessageID: id, VendorMessageID: id, NativeTurn: turn, State: state}
}

func waitForHarnessCalls(t *testing.T, calls func() int, want int) {
	t.Helper()
	require.Eventually(t, func() bool { return calls() == want }, time.Second, time.Millisecond)
}

func TestLLMHarnessRuntimeKeepsOneAdapterAcrossTurns(t *testing.T) {
	adapter := newFakeLLMHarnessAdapter()
	var commits []LLMHarnessCommit
	runtime, err := NewLLMHarnessRuntime(t.Context(), LLMHarnessCodex, adapter, LLMHarnessStart{}, func(_ context.Context, commit LLMHarnessCommit) (string, error) {
		commits = append(commits, commit)
		return lastHarnessReply(commit.Messages), nil
	})
	require.NoError(t, err)
	defer runtime.Close(context.Background())

	require.NoError(t, runtime.Enqueue(t.Context(), "one", harnessPrompt("first")))
	waitForHarnessCalls(t, func() int { adapter.mu.Lock(); defer adapter.mu.Unlock(); return len(adapter.starts) }, 1)
	adapter.events <- LLMHarnessTurn{NativeTurnID: "turn-1", State: LLMHarnessTurnStarted}
	adapter.events <- harnessLifecycle("one", "turn-1", LLMHarnessMessageStarted)
	adapter.events <- LLMHarnessTextDelta{Block: 0, Delta: "reply one"}
	adapter.events <- LLMHarnessTurn{NativeTurnID: "turn-1", State: LLMHarnessTurnCompleted}
	adapter.events <- LLMHarnessCompleted{NativeTurnID: "turn-1"} // duplicate terminal form
	reply, err := runtime.Await(t.Context(), "one")
	require.NoError(t, err)
	assert.Equal(t, "reply one", reply)

	require.NoError(t, runtime.Enqueue(t.Context(), "two", harnessPrompt("second")))
	waitForHarnessCalls(t, func() int { adapter.mu.Lock(); defer adapter.mu.Unlock(); return len(adapter.starts) }, 2)
	adapter.events <- LLMHarnessTurn{NativeTurnID: "turn-2", State: LLMHarnessTurnStarted}
	adapter.events <- harnessLifecycle("two", "turn-2", LLMHarnessMessageStarted)
	adapter.events <- LLMHarnessTextDelta{Block: 0, Delta: "reply two"}
	adapter.events <- LLMHarnessCompleted{NativeTurnID: "turn-2"}
	reply, err = runtime.Await(t.Context(), "two")
	require.NoError(t, err)
	assert.Equal(t, "reply two", reply)

	adapter.mu.Lock()
	assert.Equal(t, 1, adapter.startCalls)
	adapter.mu.Unlock()
	require.Len(t, commits, 2)
	assert.Equal(t, []string{"one"}, commits[0].DaggerMessageIDs)
	assert.Equal(t, []string{"two"}, commits[1].DaggerMessageIDs)
}

func TestLLMHarnessRuntimeSteerAckWaitsForConsumption(t *testing.T) {
	adapter := newFakeLLMHarnessAdapter()
	runtime, err := NewLLMHarnessRuntime(t.Context(), LLMHarnessCodex, adapter, LLMHarnessStart{}, nil)
	require.NoError(t, err)
	defer runtime.Close(context.Background())

	require.NoError(t, runtime.Enqueue(t.Context(), "one", harnessPrompt("first")))
	waitForHarnessCalls(t, func() int { adapter.mu.Lock(); defer adapter.mu.Unlock(); return len(adapter.starts) }, 1)
	adapter.events <- LLMHarnessTurn{NativeTurnID: "turn", State: LLMHarnessTurnStarted}
	adapter.events <- harnessLifecycle("one", "turn", LLMHarnessMessageStarted)
	require.NoError(t, runtime.Enqueue(t.Context(), "two", harnessPrompt("steer")))
	waitForHarnessCalls(t, func() int { adapter.mu.Lock(); defer adapter.mu.Unlock(); return len(adapter.steers) }, 1)

	readCtx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = runtime.Delivery(readCtx, "two")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	adapter.events <- harnessLifecycle("two", "turn", LLMHarnessMessageQueued)
	readCtx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = runtime.Delivery(readCtx, "two")
	require.ErrorIs(t, err, context.DeadlineExceeded)

	adapter.events <- harnessLifecycle("two", "turn", LLMHarnessMessageStarted)
	delivery, err := runtime.Delivery(t.Context(), "two")
	require.NoError(t, err)
	assert.Equal(t, AgentMessageSteered, delivery)
	adapter.events <- LLMHarnessTextDelta{Block: 0, Delta: "shared"}
	adapter.events <- LLMHarnessCompleted{NativeTurnID: "turn"}
	first, err := runtime.Await(t.Context(), "one")
	require.NoError(t, err)
	second, err := runtime.Await(t.Context(), "two")
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestLLMHarnessRuntimeExpectedTurnRetryWinsLifecycleRace(t *testing.T) {
	adapter := newFakeLLMHarnessAdapter()
	runtime, err := NewLLMHarnessRuntime(t.Context(), LLMHarnessCodex, adapter, LLMHarnessStart{}, nil)
	require.NoError(t, err)
	defer runtime.Close(context.Background())

	require.NoError(t, runtime.Enqueue(t.Context(), "one", harnessPrompt("first")))
	waitForHarnessCalls(t, func() int { adapter.mu.Lock(); defer adapter.mu.Unlock(); return len(adapter.starts) }, 1)
	adapter.events <- LLMHarnessTurn{NativeTurnID: "old", State: LLMHarnessTurnStarted}
	adapter.events <- harnessLifecycle("one", "old", LLMHarnessMessageStarted)

	adapter.mu.Lock()
	adapter.steerErr = ErrCodexTurnMismatch
	adapter.startTurnHook = func(input LLMHarnessInput) {
		if input.DaggerMessageID == "two" {
			// Emit definitive lifecycle before StartTurn returns. The runtime must
			// already have changed its pending intent from STEERED to STARTED.
			adapter.events <- LLMHarnessTurn{NativeTurnID: "new", State: LLMHarnessTurnStarted}
			adapter.events <- harnessLifecycle("two", "new", LLMHarnessMessageStarted)
		}
	}
	adapter.mu.Unlock()
	require.NoError(t, runtime.Enqueue(t.Context(), "two", harnessPrompt("retry")))
	waitForHarnessCalls(t, func() int { adapter.mu.Lock(); defer adapter.mu.Unlock(); return len(adapter.starts) }, 2)
	delivery, err := runtime.Delivery(t.Context(), "two")
	require.NoError(t, err)
	assert.Equal(t, AgentMessageStarted, delivery)

	adapter.events <- LLMHarnessTextDelta{Block: 0, Delta: "new reply"}
	adapter.events <- LLMHarnessCompleted{NativeTurnID: "new"}
	_, err = runtime.Await(t.Context(), "two")
	require.NoError(t, err)
}

func TestLLMHarnessRuntimeInterruptCancelsUnconsumedSteer(t *testing.T) {
	adapter := newFakeLLMHarnessAdapter()
	runtime, err := NewLLMHarnessRuntime(t.Context(), LLMHarnessCodex, adapter, LLMHarnessStart{}, nil)
	require.NoError(t, err)
	defer runtime.Close(context.Background())

	require.NoError(t, runtime.Enqueue(t.Context(), "one", harnessPrompt("first")))
	waitForHarnessCalls(t, func() int { adapter.mu.Lock(); defer adapter.mu.Unlock(); return len(adapter.starts) }, 1)
	adapter.events <- LLMHarnessTurn{NativeTurnID: "turn", State: LLMHarnessTurnStarted}
	adapter.events <- harnessLifecycle("one", "turn", LLMHarnessMessageStarted)
	require.NoError(t, runtime.Enqueue(t.Context(), "two", harnessPrompt("steer")))
	waitForHarnessCalls(t, func() int { adapter.mu.Lock(); defer adapter.mu.Unlock(); return len(adapter.steers) }, 1)
	require.NoError(t, runtime.Interrupt(t.Context()))
	adapter.events <- harnessLifecycle("two", "turn", LLMHarnessMessageCancelled)
	adapter.events <- LLMHarnessInterrupted{NativeTurnID: "turn"}

	_, err = runtime.Delivery(t.Context(), "two")
	require.Error(t, err)
	assert.False(t, errors.Is(err, context.Canceled))
	_, err = runtime.Await(t.Context(), "two")
	require.Error(t, err)
	adapter.mu.Lock()
	assert.Equal(t, []string{"turn"}, adapter.interruptCalls)
	adapter.mu.Unlock()
}

func TestLLMHarnessRuntimeCheckpointMaterialization(t *testing.T) {
	adapter := newFakeLLMHarnessAdapter()
	var checkpoint LLMHarnessCommit
	runtime, err := NewLLMHarnessRuntime(t.Context(), LLMHarnessCodex, adapter, LLMHarnessStart{}, func(_ context.Context, commit LLMHarnessCommit) (string, error) {
		checkpoint = commit
		return lastHarnessReply(commit.Messages), nil
	})
	require.NoError(t, err)
	defer runtime.Close(context.Background())

	require.NoError(t, runtime.Enqueue(t.Context(), "one", harnessPrompt("first")))
	waitForHarnessCalls(t, func() int { adapter.mu.Lock(); defer adapter.mu.Unlock(); return len(adapter.starts) }, 1)
	adapter.events <- LLMHarnessTurn{NativeTurnID: "turn", State: LLMHarnessTurnStarted}
	adapter.events <- harnessLifecycle("one", "turn", LLMHarnessMessageStarted)
	adapter.events <- LLMHarnessThinkingDelta{Block: 0, Delta: "think", Signature: "sig"}
	adapter.events <- LLMHarnessTextDelta{Block: 1, Delta: "answer"}
	adapter.events <- LLMHarnessToolCall{Block: 2, CallID: "call", Name: "tool", Arguments: JSON(`{"x":1}`), Source: LLMHarnessToolSourceMCP}
	adapter.events <- LLMHarnessToolResult{CallID: "call", Text: "result"}
	adapter.events <- LLMHarnessUsage{Usage: LLMTokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}}
	adapter.events <- LLMHarnessCompleted{NativeTurnID: "turn"}
	_, err = runtime.Await(t.Context(), "one")
	require.NoError(t, err)

	assert.Equal(t, adapter.native, checkpoint.NativeState)
	assert.Equal(t, []LLMHarnessMessageCorrelation{{DaggerMessageID: "one", VendorMessageID: "one"}}, checkpoint.Correlations)
	require.Len(t, checkpoint.Messages, 2)
	assert.Equal(t, LLMMessageRoleAssistant, checkpoint.Messages[0].Role)
	assert.Equal(t, "think", checkpoint.Messages[0].Content[0].Text)
	assert.Equal(t, "answer", checkpoint.Messages[0].TextContent())
	assert.Equal(t, int64(5), checkpoint.Messages[0].TokenUsage.TotalTokens)
	assert.Equal(t, "result", checkpoint.Messages[1].ToolResultContent())
}
