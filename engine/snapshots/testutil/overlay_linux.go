package testutil

import (
	"os"
	"testing"

	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay"
)

// NewOverlayStore is NewStore over the overlayfs snapshotter, for tests that
// need real overlay semantics (upperdirs, whiteouts, opaque directories). It
// skips unless running as root, since overlay mounts need privileges.
func NewOverlayStore(t testing.TB) *Store {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("overlayfs snapshotter tests require root")
	}
	return newStore(t, "overlayfs", func(root string) (ctdsnapshots.Snapshotter, error) {
		return overlay.NewSnapshotter(root)
	})
}
