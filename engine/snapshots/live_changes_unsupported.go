//go:build !linux

package snapshots

import "context"

func (sn *mergeSnapshotter) applyLiveChanges(_ context.Context, _, _, _, _ string) error {
	return ErrLiveChangesUnsupported
}
