package engine

import (
	"context"
	"errors"
	"fmt"
)

// ErrSnapshotShareEvaluation is returned by every boundary a snapshot-share
// preparation is forbidden to cross. It is an invariant diagnostic, not a
// supported outcome: the decoder audit and the reference preflight are what
// keep a known context-dependent failure out of a shared decode attempt, and
// a guard that trips means one of those missed a case.
var ErrSnapshotShareEvaluation = errors.New("snapshot share preparation cannot evaluate, execute, start a service or request content")

type snapshotSharePreparationKey struct{}

// WithSnapshotSharePreparation marks a context as a sharing worker's
// preparation context. The marker is preserved through slot, ancestor and
// detached cleanup contexts; it never enters a decoded value, a schema
// builder or an unmarked foreground waiter's context.
func WithSnapshotSharePreparation(ctx context.Context) context.Context {
	return context.WithValue(ctx, snapshotSharePreparationKey{}, true)
}

// IsSnapshotSharePreparation reports whether ctx carries the marker.
func IsSnapshotSharePreparation(ctx context.Context) bool {
	marked, _ := ctx.Value(snapshotSharePreparationKey{}).(bool)
	return marked
}

// CheckSnapshotSharePreparation refuses the named operation under the marker.
// Guards never grant authority: an accidental ClientScope does not bypass
// them, and an unmarked context is unaffected.
func CheckSnapshotSharePreparation(ctx context.Context, operation string) error {
	if !IsSnapshotSharePreparation(ctx) {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrSnapshotShareEvaluation, operation)
}
