package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAgentStopRejectsWaitCycles(t *testing.T) {
	t.Parallel()

	for _, self := range []bool{true, false} {
		name := "cross-agent"
		if self {
			name = "self"
		}
		t.Run(name, func(t *testing.T) {
			ars, rtA, rtB, ctxA, ctxB := twoAgentRegistry(t)
			caller := ctxA
			if !self {
				release, err := ars.beginAgentWait(ctxA, rtB, "awaiting a reply")
				require.NoError(t, err)
				defer release()
				caller = ctxB
			}
			rtA.started = true
			rtA.stepping = true
			ctx, cancel := context.WithTimeout(caller, time.Second)
			defer cancel()
			err := rtA.Stop(ctx, false, nil, AgentStopExplicit)
			if self {
				require.ErrorContains(t, err, "cannot block on itself")
			} else {
				require.ErrorContains(t, err, "would deadlock")
			}
			require.False(t, rtA.stopRequested, "a refused stop must not mutate the target")
			require.Empty(t, rtA.stopReason)
			require.Equal(t, AgentStateRunning, rtA.State())
		})
	}
}

func TestAgentStopRegistersWaitUntilReturn(t *testing.T) {
	t.Parallel()

	ars, rtA, rtB, ctxA, ctxB := twoAgentRegistry(t)
	rtB.started = true
	ctx, cancel := context.WithCancel(ctxA)
	defer cancel()
	stopped := make(chan error, 1)
	go func() {
		stopped <- rtB.Stop(ctx, false, nil, AgentStopExplicit)
	}()

	// Stop wakes the target only after registering its wait and requesting
	// shutdown. Keep the loop unfinished so another agent can try to wait.
	select {
	case <-rtB.wake:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not wake the target")
	}
	release, err := ars.beginAgentWait(ctxB, rtA, "awaiting a reply")
	if err == nil {
		release()
	}
	require.ErrorContains(t, err, "would deadlock")

	cancel()
	require.ErrorIs(t, <-stopped, context.Canceled)
	release, err = ars.beginAgentWait(ctxB, rtA, "awaiting a reply")
	require.NoError(t, err, "returning from stop must release its wait edge")
	release()
}
