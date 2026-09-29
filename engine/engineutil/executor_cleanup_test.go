package engineutil

import (
	"context"
	"errors"
	"sync"
	"testing"

	gatewayapi "github.com/dagger/dagger/internal/buildkit/frontend/gateway/pb"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/stretchr/testify/require"
)

func TestExecCleanupErrorPreservesExit(t *testing.T) {
	for _, exit := range []bool{false, true} {
		name := "cleanup only"
		if exit {
			name = "cleanup and exit"
		}
		t.Run(name, func(t *testing.T) {
			cleanupCause := errors.New("container cgroup still busy")
			var runErr error
			if exit {
				runErr = &gatewayapi.ExitError{ExitCode: 137}
			}
			c := &Client{Opts: &Opts{running: map[string]*execState{}, runningMu: new(sync.RWMutex)}}
			state := &execState{id: "cleanup-test", done: make(chan struct{}), cleanups: new(cleanups.Cleanups)}
			state.cleanups.Add("injected container cleanup", func() error { return cleanupCause })
			err := c.run(t.Context(), state, namedSetupFunc{"fake run", func(context.Context, *execState) error { return runErr }})
			require.ErrorIs(t, err, cleanupCause)
			if exit {
				require.ErrorIs(t, err, runErr)
			}
			var cleanup *ExecCleanupError
			require.ErrorAs(t, err, &cleanup, "cleanup must remain distinguishable from expected process exit")
			require.ErrorIs(t, ExecCleanupErrors(err), cleanupCause)
			require.Empty(t, c.running)
		})
	}
}

func TestExecCleanupErrorsJoinedTree(t *testing.T) {
	first, second := errors.New("first cleanup"), errors.New("second cleanup")
	exit := &gatewayapi.ExitError{ExitCode: 137}
	joined := errors.Join(exit, &ExecCleanupError{Err: first}, &ExecCleanupError{Err: second})
	cleanup := ExecCleanupErrors(joined)
	require.ErrorIs(t, cleanup, first)
	require.ErrorIs(t, cleanup, second)
	require.NotErrorIs(t, cleanup, exit)
	require.Nil(t, ExecCleanupErrors(exit))
}
