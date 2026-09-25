package tracesource

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/dagger/dagger/engine/archive"
	"github.com/stretchr/testify/require"
)

func TestSelectOnlyFallsBackBeforeImport(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		fallback bool
	}{
		{"miss", archive.ErrCleanMiss, true},
		{"unavailable", archive.ErrTransient, true},
		{"unsupported", &archive.RequestError{Kind: archive.ErrorTransient, StatusCode: http.StatusNotImplemented}, true},
		{"corrupt", archive.ErrCorrupt, false},
		{"state", archive.ErrState, false},
		{"ambiguous", &archive.RequestError{Kind: archive.ErrorState, Failure: archive.FailureAmbiguous}, false},
		{"unauthorized", &archive.RequestError{Kind: archive.ErrorTransient, StatusCode: http.StatusUnauthorized}, false},
		{"forbidden", &archive.RequestError{Kind: archive.ErrorTransient, StatusCode: http.StatusForbidden}, false},
		{"bad request", &archive.RequestError{Kind: archive.ErrorTransient, StatusCode: http.StatusBadRequest}, false},
		{"canceled", errors.Join(archive.ErrTransient, context.Canceled), false},
		{"deadline", errors.Join(archive.ErrTransient, context.DeadlineExceeded), false},
		{"unknown", errors.New("connection configuration invalid"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened, closed := false, false
			_, _, err := Select(t.Context(), "", func(context.Context) (Source, func() error, error) {
				return nil, func() error { closed = true; return nil }, tc.err
			}, func(context.Context) (Source, func() error, error) {
				require.True(t, closed)
				opened = true
				return nil, nil, nil
			})
			require.Equal(t, tc.fallback, opened)
			if !tc.fallback {
				require.ErrorIs(t, err, tc.err)
			}
		})
	}
}

func TestSelectPinsAndCleanup(t *testing.T) {
	remote := func(context.Context) (Source, func() error, error) {
		t.Fatal("must not open Cloud")
		return nil, nil, nil
	}
	t.Run("generation", func(t *testing.T) {
		_, _, err := Select(t.Context(), "pinned", func(context.Context) (Source, func() error, error) { return nil, nil, archive.ErrCleanMiss }, remote)
		require.ErrorContains(t, err, "generation pinned")
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, err := Select(ctx, "", func(context.Context) (Source, func() error, error) { return nil, nil, archive.ErrCleanMiss }, remote)
		require.Error(t, err)
	})
	t.Run("cleanup failure", func(t *testing.T) {
		failure := errors.New("engine close")
		_, _, err := Select(t.Context(), "", func(context.Context) (Source, func() error, error) {
			return nil, func() error { return failure }, archive.ErrCleanMiss
		}, remote)
		require.ErrorIs(t, err, failure)
	})
	t.Run("local lifetime", func(t *testing.T) {
		closed := false
		_, close, err := Select(t.Context(), "", func(context.Context) (Source, func() error, error) {
			return nil, func() error { closed = true; return nil }, nil
		}, remote)
		require.NoError(t, err)
		require.False(t, closed, "selection must keep the source alive for lazy reads")
		require.NoError(t, close())
		require.True(t, closed)
	})
}
