package snapshots

import (
	"context"
	"sync"

	"github.com/dagger/dagger/internal/buildkit/util/bklog"
)

// maxPendingReleases bounds the read-only mount releases running in the
// background. A release beyond it waits for one of them to finish.
const maxPendingReleases = 64

// backgroundReleases runs releases off their callers' paths, at most max at a
// time, and logs their errors.
type backgroundReleases struct {
	slots    chan struct{}
	wg       sync.WaitGroup
	logError func(error)

	mu     sync.Mutex
	closed bool
}

func newBackgroundReleases(max int) *backgroundReleases {
	return &backgroundReleases{slots: make(chan struct{}, max), logError: logReleaseError}
}

func (b *backgroundReleases) run(release func() error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		b.logError(release())
		return
	}
	b.wg.Add(1)
	b.mu.Unlock()

	b.slots <- struct{}{}
	go func() {
		defer func() {
			<-b.slots
			b.wg.Done()
		}()
		b.logError(release())
	}()
}

// wait waits for the releases in flight. Releases started afterwards run on
// their callers' paths.
func (b *backgroundReleases) wait() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	b.wg.Wait()
}

func logReleaseError(err error) {
	if err != nil {
		bklog.G(context.TODO()).WithError(err).Error("failed to release read-only mount")
	}
}

// ReadOnlyMountReleaser is a snapshot manager that shares read-only mounts
// and releases them in the background.
type ReadOnlyMountReleaser interface {
	// ReleaseIdleSharedMounts releases the shared read-only mounts nobody is
	// using, so that garbage collection can reclaim the snapshots their
	// views held. It returns once they are released.
	ReleaseIdleSharedMounts() error
	// WaitForBackgroundReleases releases the shared read-only mounts nobody
	// is using and waits for the releases running in the background, such
	// as before the metadata DBs close. Mounts released later are released
	// on their callers' paths, and are no longer shared.
	WaitForBackgroundReleases()
}

func (cm *snapshotManager) WaitForBackgroundReleases() {
	logReleaseError(cm.sharedMounts.close())
	cm.backgroundReleases.wait()
}
