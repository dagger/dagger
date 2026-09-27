package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestNativeCommitFallback(t *testing.T) {
	reason := nativeCommitUnsupportedReason("shallow-history")
	cleanup := errors.New("unmount failed")
	for _, tc := range []struct {
		name     string
		err      error
		fallback bool
	}{
		{"success", nil, false},
		{"reason", reason, true},
		{"sentinel", errNativeCommitUnsupported, true},
		{"wrapped reason", fmt.Errorf("mount: %w", reason), true},
		{"joined reason only", errors.Join(reason, nil), true},
		{"joined unsupported reasons", errors.Join(reason, nativeCommitUnsupportedReason("gitlink-change")), true},
		{"cleanup", cleanup, false},
		{"reason and cleanup", errors.Join(reason, cleanup), false},
		{"wrapped reason and cleanup", fmt.Errorf("mount: %w", errors.Join(fmt.Errorf("operation: %w", reason), cleanup)), false},
		{"multiple wrapped errors", fmt.Errorf("operation: %w; cleanup: %w", reason, cleanup), false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"reason and cancellation", errors.Join(reason, context.Canceled), false},
		{"reason and deadline", errors.Join(reason, context.DeadlineExceeded), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.fallback, nativeCommitFallback(tc.err))
		})
	}
}

func TestNativeFallbackPolicy(t *testing.T) {
	reason := nativeCommitUnsupportedReason("shallow-history")
	failure := errors.New("git [read-tree abc]: exit status 128: fatal: failed to unpack tree object abc")
	for _, tc := range []struct {
		name     string
		err      error
		fallback bool
	}{
		{"success", nil, false},
		// Nothing to commit is the answer, not a failure of the optimization.
		{"nothing to commit", ErrNothingToCommit, false},
		{"wrapped nothing to commit", fmt.Errorf("native commit: %w", ErrNothingToCommit), false},
		{"unsupported", reason, true},
		// Real failures of the optimization fall back to the legacy path.
		{"failure", failure, true},
		{"reason and cleanup", errors.Join(reason, errors.New("unmount failed")), true},
		{"unexpected transaction object", fmt.Errorf("unexpected transaction object %q", "pack/tmp_pack_x"), true},
		// A deadline from some internal context is not the caller's.
		{"internal deadline", fmt.Errorf("mount: %w", context.DeadlineExceeded), true},
		{"internal cancellation", context.Canceled, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			defer provider.Shutdown(t.Context())
			_, span := provider.Tracer("native-test").Start(t.Context(), "native")
			require.Equal(t, tc.fallback, nativeFallback(t.Context(), span, "fallback_reason", tc.err))
			span.End()
			attrs := map[string]string{}
			for _, attr := range recorder.Ended()[0].Attributes() {
				attrs[string(attr.Key)] = attr.Value.AsString()
			}
			if tc.fallback {
				require.Equal(t, tc.err.Error(), attrs["fallback_reason"])
			} else {
				require.NotContains(t, attrs, "fallback_reason")
			}
		})
	}
	// The caller's own cancellation always surfaces, whatever the error.
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, cancelExpired := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancelExpired()
	for _, ctx := range []context.Context{canceled, expired} {
		for _, err := range []error{reason, failure, ctx.Err()} {
			require.False(t, nativeFallback(ctx, trace.SpanFromContext(ctx), "fallback_reason", err))
		}
	}
}

func TestGitCommitStagePaths(t *testing.T) {
	require.Equal(t, []string{":literal", "a/file", "b", "gone/file"}, commitStagePaths(&ChangesetPaths{
		Added:      []string{"a/", "a/file", "empty/", ":literal"},
		Modified:   []string{"b", "a/file"},
		AllRemoved: []string{"gone/", "gone/file"},
	}))
}
