package flightcontrol

import (
	"context"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestCached(t *testing.T) {
	var g CachedGroup[int]

	ctx := context.TODO()

	v, err := g.Do(ctx, "11", func(ctx context.Context) (int, error) {
		return 1, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, v)

	v, err = g.Do(ctx, "22", func(ctx context.Context) (int, error) {
		return 2, nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, v)

	didCall := false
	v, err = g.Do(ctx, "11", func(ctx context.Context) (int, error) {
		didCall = true
		return 3, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, v)
	require.Equal(t, false, didCall)

	// by default, errors are not cached
	_, err = g.Do(ctx, "33", func(ctx context.Context) (int, error) {
		return 0, errors.Errorf("some error")
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "some error")

	v, err = g.Do(ctx, "33", func(ctx context.Context) (int, error) {
		return 3, nil
	})

	require.NoError(t, err)
	require.Equal(t, 3, v)
}

func TestCachedError(t *testing.T) {
	var g CachedGroup[string]
	g.CacheError = true

	ctx := context.TODO()

	_, err := g.Do(ctx, "11", func(ctx context.Context) (string, error) {
		return "", errors.Errorf("first error")
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "first error")

	_, err = g.Do(ctx, "11", func(ctx context.Context) (string, error) {
		return "never-ran", nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "first error")

	// context errors are never cached
	ctx, cancel := context.WithTimeoutCause(context.TODO(), 10*time.Millisecond, nil)
	defer cancel()
	_, err = g.Do(ctx, "22", func(ctx context.Context) (string, error) {
		select {
		case <-ctx.Done():
			return "", context.Cause(ctx)
		case <-time.After(10 * time.Second):
			return "", errors.Errorf("unexpected error")
		}
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "context deadline exceeded")

	select {
	case <-ctx.Done():
	default:
		require.Fail(t, "expected context to be done")
	}

	v, err := g.Do(ctx, "22", func(ctx context.Context) (string, error) {
		return "did-run", nil
	})
	require.NoError(t, err)
	require.Equal(t, "did-run", v)
}
