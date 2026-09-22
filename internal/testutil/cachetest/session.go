// Package cachetest provides cache lifecycle helpers for tests.
package cachetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type sessionReleaser interface {
	ReleaseSession(context.Context, string) error
	WaitSessionRelease(context.Context, string) error
}

// ReleaseSessionAndWait releases a session and waits up to five seconds for its
// delegated cleanup before a test observes collection. Do not use it while a
// test intentionally keeps a session operation blocked: ReleaseSession alone
// allows those tests to observe the closing session before releasing the worker.
func ReleaseSessionAndWait(t testing.TB, ctx context.Context, cache sessionReleaser, sessionID string) {
	t.Helper()
	require.NoError(t, cache.ReleaseSession(ctx, sessionID), "release session %q", sessionID)
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, cache.WaitSessionRelease(waitCtx, sessionID), "wait for delegated cleanup of session %q", sessionID)
}
