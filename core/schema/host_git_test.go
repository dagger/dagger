package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/engineutil"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

// A host checkout built on its last reconstruction only spares work: when
// that fails, the checkout is packed whole, as one with no earlier
// reconstruction is. A canceled call and a checkout that moved while it was
// packed fail as they are, and a whole pack that fails isn't tried again.
func TestReconstructOnLastOrWhole(t *testing.T) {
	whole := &core.Directory{}
	haves := []string{"abc"}
	type attempt struct {
		relative bool
	}
	run := func(ctx context.Context, haves []string, fail func(relative bool) error) (*core.Directory, []attempt, error) {
		var attempts []attempt
		dir, err := reconstructOnLastOrWhole(ctx, "/repo", nil, haves, func(_ bkcache.ImmutableRef, haves []string) (*core.Directory, error) {
			relative := haves != nil
			attempts = append(attempts, attempt{relative: relative})
			if err := fail(relative); err != nil {
				return nil, err
			}
			return whole, nil
		})
		return dir, attempts, err
	}

	t.Run("a failed relative pack falls back to a whole one", func(t *testing.T) {
		dir, attempts, err := run(t.Context(), haves, func(relative bool) error {
			if relative {
				return errors.New("unbundle checkout pack: missing prerequisites")
			}
			return nil
		})
		require.NoError(t, err)
		require.Same(t, whole, dir)
		require.Equal(t, []attempt{{relative: true}, {relative: false}}, attempts)
	})
	t.Run("a whole pack that fails fails once", func(t *testing.T) {
		_, attempts, err := run(t.Context(), nil, func(bool) error { return errors.New("pack failed") })
		require.ErrorContains(t, err, "pack failed")
		require.Equal(t, []attempt{{relative: false}}, attempts)
	})
	t.Run("a moved checkout is not retried", func(t *testing.T) {
		_, attempts, err := run(t.Context(), haves, func(bool) error { return engineutil.ErrGitCheckoutStateChanged })
		require.ErrorIs(t, err, engineutil.ErrGitCheckoutStateChanged)
		require.Len(t, attempts, 1)
	})
	t.Run("a canceled call is not retried", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, attempts, err := run(ctx, haves, func(bool) error { return context.Canceled })
		require.ErrorIs(t, err, context.Canceled)
		require.Len(t, attempts, 1)
	})
}
