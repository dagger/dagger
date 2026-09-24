package core

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
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

func TestGitCommitStagePaths(t *testing.T) {
	require.Equal(t, []string{":literal", "a/file", "b", "gone/file"}, commitStagePaths(&ChangesetPaths{
		Added:      []string{"a/", "a/file", "empty/", ":literal"},
		Modified:   []string{"b", "a/file"},
		AllRemoved: []string{"gone/", "gone/file"},
	}))
}
