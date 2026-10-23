package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

// A failing OnBodyDone hook fails its body's evaluation: the whole op or the
// group stays unconsumed, the output revision still moves, and the next
// evaluation runs the body again and succeeds once the hook does.
func TestLazyStateBodyDoneFailure(t *testing.T) {
	const group dagql.LazyGroupKey = "group"
	for _, c := range []struct {
		name     string
		group    dagql.LazyGroupKey
		evaluate func(context.Context, *LazyState, func(context.Context) error) error
		consumed func(*LazyState) bool
	}{
		{
			name:  "Evaluate",
			group: dagql.LazyGroupWhole,
			evaluate: func(ctx context.Context, state *LazyState, run func(context.Context) error) error {
				return state.Evaluate(ctx, "test.whole", run)
			},
			consumed: func(state *LazyState) bool { return state.lazyInitComplete.Load() },
		},
		{
			name:  "EvaluateGroup",
			group: group,
			evaluate: func(ctx context.Context, state *LazyState, run func(context.Context) error) error {
				return state.EvaluateGroup(ctx, "test.group", group, run)
			},
			consumed: func(state *LazyState) bool { return state.groups[group].done.Load() },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := t.Context()
			state := NewLazyState()
			hookErr := errors.New("hook failed")
			failing := true
			var hooked []dagql.LazyGroupKey
			state.OnBodyDone(func(_ context.Context, g dagql.LazyGroupKey) error {
				hooked = append(hooked, g)
				if failing {
					return hookErr
				}
				return nil
			})
			runs := 0
			run := func(context.Context) error {
				runs++
				return nil
			}

			before := state.outputRevision.Load()
			require.ErrorIs(t, c.evaluate(ctx, &state, run), hookErr)
			require.False(t, c.consumed(&state))
			require.False(t, state.lazyInitComplete.Load())
			require.Greater(t, state.outputRevision.Load(), before)

			failing = false
			require.NoError(t, c.evaluate(ctx, &state, run))
			require.True(t, c.consumed(&state))
			require.Equal(t, 2, runs)
			require.Equal(t, []dagql.LazyGroupKey{c.group, c.group}, hooked)

			require.NoError(t, c.evaluate(ctx, &state, run))
			require.Equal(t, 2, runs, "a consumed body does not run again")
		})
	}
}
