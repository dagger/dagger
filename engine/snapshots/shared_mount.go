package snapshots

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
)

const (
	// DefaultSharedMountLinger is how long a shared read-only mount nobody is
	// using is kept for its next reader.
	DefaultSharedMountLinger = 30 * time.Second
	// DefaultMaxIdleSharedMounts bounds the shared read-only mounts nobody is
	// using that are kept; beyond it, the least recently used are released.
	DefaultMaxIdleSharedMounts = 512
)

// SharedMounter is a ref whose read-only mounts are shared across the engine:
// every reader of its snapshot uses one view of it and one local mount, which
// are kept while any reader uses them and, once none does, for a linger
// period. Immutable refs implement it.
type SharedMounter interface {
	// MountShared returns the root of the snapshot's shared read-only mount
	// and the mount it came from. release ends this reader's use of it; it
	// must be called once.
	MountShared(ctx context.Context) (root string, m mount.Mount, release func(), err error)
}

// sharedMounts keeps one read-only mount per snapshot for all its readers.
// A mount nobody uses is kept for linger, and at most maxIdle such mounts are
// kept, the least recently used released first. Releasing a mount unmounts it
// and then deletes its view lease.
type sharedMounts struct {
	linger  time.Duration
	maxIdle int
	// release runs a mount's release off the path of whoever let it go.
	release func(func() error)

	mu      sync.Mutex
	entries map[string]*sharedMount
	// idle holds the mounts nobody uses, most recently used first.
	idle   *list.List
	closed bool
	// evicting counts the mounts taken out of the set whose release has not
	// finished; evicted is signaled when it drops to zero.
	evicting int
	evicted  *sync.Cond
}

type sharedMount struct {
	// users counts the readers using or mounting it; guarded by
	// sharedMounts.mu, as are idleElem, idleGen and timer.
	users    int
	idleElem *list.Element
	idleGen  uint64
	timer    *time.Timer

	// ready is closed once the mount below is made or has failed.
	ready   chan struct{}
	err     error
	root    string
	mnt     mount.Mount
	unmount func() error
}

func newSharedMounts(linger time.Duration, maxIdle int, release func(func() error)) *sharedMounts {
	if linger <= 0 {
		linger = DefaultSharedMountLinger
	}
	if maxIdle <= 0 {
		maxIdle = DefaultMaxIdleSharedMounts
	}
	s := &sharedMounts{
		linger:  linger,
		maxIdle: maxIdle,
		release: release,
		entries: map[string]*sharedMount{},
		idle:    list.New(),
	}
	s.evicted = sync.NewCond(&s.mu)
	return s
}

// acquire returns the shared mount for key, making it with open if no reader
// has. Once the set is closed, each reader gets its own mount, released when
// that reader is done.
func (s *sharedMounts) acquire(key string, open func() (string, mount.Mount, func() error, error)) (string, mount.Mount, func(), error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		root, mnt, unmount, err := open()
		if err != nil {
			return "", mount.Mount{}, nil, err
		}
		var once sync.Once
		return root, mnt, func() {
			once.Do(func() { logReleaseError(unmount()) })
		}, nil
	}
	m := s.entries[key]
	if m != nil {
		m.users++
		s.unidleLocked(m)
		s.mu.Unlock()
		<-m.ready
	} else {
		m = &sharedMount{users: 1, ready: make(chan struct{})}
		s.entries[key] = m
		s.mu.Unlock()
		m.root, m.mnt, m.unmount, m.err = open()
		if m.err != nil {
			// Readers waiting on it get the error; the next one mounts again.
			s.mu.Lock()
			if s.entries[key] == m {
				delete(s.entries, key)
			}
			s.mu.Unlock()
		}
		close(m.ready)
	}
	if m.err != nil {
		s.done(key, m)
		return "", mount.Mount{}, nil, m.err
	}
	var once sync.Once
	return m.root, m.mnt, func() {
		once.Do(func() { s.done(key, m) })
	}, nil
}

// done ends one reader's use of m. When nobody uses it anymore it is kept for
// the linger period, or released now if the set is closed.
func (s *sharedMounts) done(key string, m *sharedMount) {
	s.mu.Lock()
	m.users--
	if m.users > 0 || m.err != nil {
		s.mu.Unlock()
		return
	}
	if s.closed {
		delete(s.entries, key)
		s.mu.Unlock()
		logReleaseError(m.unmount())
		return
	}
	m.idleElem = s.idle.PushFront(key)
	m.idleGen++
	gen := m.idleGen
	m.timer = time.AfterFunc(s.linger, func() { s.expire(key, m, gen) })
	var evicted []*sharedMount
	for s.idle.Len() > s.maxIdle {
		oldestKey := s.idle.Back().Value.(string)
		oldest := s.entries[oldestKey]
		s.evictLocked(oldestKey, oldest)
		evicted = append(evicted, oldest)
	}
	s.mu.Unlock()
	for _, m := range evicted {
		s.releaseEvicted(m)
	}
}

// expire releases m if it has stayed unused since the linger timer gen.
func (s *sharedMounts) expire(key string, m *sharedMount, gen uint64) {
	s.mu.Lock()
	if m.idleElem == nil || m.idleGen != gen {
		s.mu.Unlock()
		return
	}
	s.evictLocked(key, m)
	s.mu.Unlock()
	s.releaseEvicted(m)
}

// evictLocked takes the idle mount m out of the set for releaseEvicted,
// counting it until its release finishes.
func (s *sharedMounts) evictLocked(key string, m *sharedMount) {
	s.removeIdleLocked(key, m)
	s.evicting++
}

// releaseEvicted releases the evicted mount m off the caller's path.
func (s *sharedMounts) releaseEvicted(m *sharedMount) {
	s.release(func() error {
		defer func() {
			s.mu.Lock()
			s.evicting--
			if s.evicting == 0 {
				s.evicted.Broadcast()
			}
			s.mu.Unlock()
		}()
		return m.unmount()
	})
}

// unidleLocked takes m off the idle list for a new reader.
func (s *sharedMounts) unidleLocked(m *sharedMount) {
	if m.idleElem == nil {
		return
	}
	s.idle.Remove(m.idleElem)
	m.idleElem = nil
	m.timer.Stop()
}

// removeIdleLocked forgets the idle mount m; the caller releases it.
func (s *sharedMounts) removeIdleLocked(key string, m *sharedMount) {
	s.unidleLocked(m)
	if s.entries[key] == m {
		delete(s.entries, key)
	}
}

// releaseIdle releases every mount nobody uses, and waits for the releases of
// mounts already evicted, so that their views no longer hold their snapshots,
// as before garbage collection.
func (s *sharedMounts) releaseIdle() error {
	s.mu.Lock()
	var idle []*sharedMount
	for e := s.idle.Front(); e != nil; {
		next := e.Next()
		key := e.Value.(string)
		m := s.entries[key]
		s.removeIdleLocked(key, m)
		idle = append(idle, m)
		e = next
	}
	s.mu.Unlock()
	var errs []error
	for _, m := range idle {
		errs = append(errs, m.unmount())
	}
	s.mu.Lock()
	for s.evicting > 0 {
		s.evicted.Wait()
	}
	s.mu.Unlock()
	return errors.Join(errs...)
}

// close releases the idle mounts. Mounts in use are released when their last
// reader is done, and later readers each get their own mount.
func (s *sharedMounts) close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return s.releaseIdle()
}

func (sr *immutableRef) MountShared(ctx context.Context) (string, mount.Mount, func(), error) {
	// The mount outlives this reader, so it must not end with its context.
	ctx = context.WithoutCancel(ctx)
	return sr.cm.sharedMounts.acquire(sr.SnapshotID(), func() (string, mount.Mount, func() error, error) {
		mountable, err := sr.Mount(ctx, true)
		if err != nil {
			return "", mount.Mount{}, nil, err
		}
		ms, releaseView, err := mountable.Mount()
		if err != nil {
			return "", mount.Mount{}, nil, err
		}
		if len(ms) == 0 {
			return "", mount.Mount{}, nil, errors.Join(errors.New("no mounts available from ref"), releaseView())
		}
		lm := sr.cm.localMounter(ms)
		root, err := lm.Mount()
		if err != nil {
			return "", mount.Mount{}, nil, errors.Join(err, releaseView())
		}
		return root, ms[0], func() error {
			return errors.Join(lm.Unmount(), releaseView())
		}, nil
	})
}

// ReleaseIdleSharedMounts releases the shared read-only mounts nobody is
// using, so that garbage collection can reclaim the snapshots their views
// held. It returns once they are released.
func (cm *snapshotManager) ReleaseIdleSharedMounts() error {
	return cm.sharedMounts.releaseIdle()
}
