package snapshots

import (
	"context"
	"sync"

	"github.com/dagger/dagger/internal/buildkit/util/bklog"
)

// maxPendingReleases bounds the read-only mount releases running in the
// background. A release beyond it waits for one of them to finish.
const maxPendingReleases = 64

// BackgroundReleaser runs the release of a read-only mount of a snapshot, its
// unmount and then the release of its view, after the caller has returned, so
// that the caller does not wait for the unmount. Immutable refs implement it:
// their mounts are read-only views, which nothing commits.
type BackgroundReleaser interface {
	ReleaseInBackground(release func() error)
}

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

func (sr *immutableRef) ReleaseInBackground(release func() error) {
	sr.cm.backgroundReleases.run(release)
}

// BackgroundReleaseWaiter is a snapshot manager that releases read-only mounts
// in the background.
type BackgroundReleaseWaiter interface {
	// WaitForBackgroundReleases waits for the read-only mount releases
	// running in the background, such as before the metadata DBs close.
	// Later releases run on their callers' paths.
	WaitForBackgroundReleases()
}

func (cm *snapshotManager) WaitForBackgroundReleases() {
	cm.backgroundReleases.wait()
}
