package clientdb

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/slog"
	"github.com/moby/locker"
)

// CollectGarbageAfter is the time after which a store is considered garbage.
const CollectGarbageAfter = time.Hour

// idleStoreLimit keeps at most 96 idle stream files open (three per store),
// enough to cover a typical fan-out batch without retaining too many sizable
// recovered span and log indexes.
const idleStoreLimit = 32

var errDBsClosed = errors.New("telemetry store registry is closed")

// DBs owns the refcounted set of open per-client telemetry stores. Stores stay
// open in a bounded LRU after their final Close so a later Open can reuse their
// recovered indexes without replaying the spill files.
type DBs struct {
	Root string

	open        map[string]*DB
	idle        *list.List
	idleByID    map[string]*list.Element
	idleLimit   int
	opening     int
	closed      bool
	mu          sync.RWMutex
	openingCond *sync.Cond

	perStoreLock *locker.Locker
	tailBudget   int64
}

// OpenStats is a measured snapshot of currently open telemetry stores.
// Each store owns exactly three stream handles, including idle cached stores.
type OpenStats struct {
	Stores  int
	Streams int
	Refs    int
}

func (r *DBs) OpenStats() OpenStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	stats := OpenStats{Stores: len(r.open), Streams: len(r.open) * 3}
	for _, store := range r.open {
		stats.Refs += store.refCount
	}
	return stats
}

func NewDBs(root string) *DBs {
	r := &DBs{
		Root:         root,
		open:         make(map[string]*DB),
		idle:         list.New(),
		idleByID:     make(map[string]*list.Element),
		idleLimit:    idleStoreLimit,
		perStoreLock: locker.New(),
		tailBudget:   telemetryTailBudget,
	}
	r.openingCond = sync.NewCond(&r.mu)
	return r
}

func (r *DBs) Open(ctx context.Context, clientID string) (*DB, error) {
	r.perStoreLock.Lock(clientID)
	defer r.perStoreLock.Unlock(clientID)

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errDBsClosed
	}
	if store := r.open[clientID]; store != nil {
		if idle := r.idleByID[clientID]; idle != nil {
			r.idle.Remove(idle)
			delete(r.idleByID, clientID)
		}
		store.refCount++
		r.mu.Unlock()
		return store, nil
	}
	r.opening++
	r.mu.Unlock()

	store, err := openStore(ctx, r.Root, clientID, r.tailBudget)

	r.mu.Lock()
	r.opening--
	r.openingCond.Broadcast()
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if r.closed {
		err = errors.Join(errDBsClosed, store.closeStreams())
		r.mu.Unlock()
		return nil, err
	}
	store.refCount = 1
	store.closeFn = func() error {
		return r.close(store)
	}
	r.open[clientID] = store
	r.mu.Unlock()
	return store, nil
}

// close assumes no registry mutex is held. The per-client lock serializes the
// refcount. Idle eviction closes streams while holding the registry mutex, so
// a concurrent Open cannot observe an evicted store as absent until its old
// writer handles have closed.
func (r *DBs) close(store *DB) error {
	r.perStoreLock.Lock(store.clientID)
	defer r.perStoreLock.Unlock(store.clientID)

	r.mu.Lock()
	defer r.mu.Unlock()
	if store.refCount <= 0 {
		return errStoreClosed
	}
	store.refCount--
	if store.refCount > 0 {
		return nil
	}

	idle := r.idle.PushBack(store)
	r.idleByID[store.clientID] = idle
	limit := r.idleLimit
	if r.closed {
		limit = 0
	}
	return r.evictIdleLocked(limit)
}

// evictIdleLocked closes the oldest idle stores until limit is met. Keeping
// mu held across closeStreams prevents Open from creating a new writer for an
// evicted client before the previous writer has closed.
func (r *DBs) evictIdleLocked(limit int) error {
	var result error
	for r.idle.Len() > limit {
		idle := r.idle.Front()
		store := idle.Value.(*DB)
		r.idle.Remove(idle)
		delete(r.idleByID, store.clientID)
		delete(r.open, store.clientID)
		result = errors.Join(result, store.closeStreams())
	}
	return result
}

// Close closes all idle cached stores and prevents new stores from opening.
// Actively referenced stores remain usable and close their streams when their
// final handle is released.
func (r *DBs) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for r.opening > 0 {
		r.openingCond.Wait()
	}
	return r.evictIdleLocked(0)
}

type storeGCGroup struct {
	clientID string
	newest   time.Time
	names    []string
}

// GC removes complete client stores whose newest stream (or transitional
// SQLite sidecar) is older than CollectGarbageAfter. Grouping files by client
// keeps a recently active stream from being separated from an older sibling.
func (r *DBs) GC(keep map[string]bool) error {
	entries, err := os.ReadDir(r.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("readdir %s: %w", r.Root, err)
	}

	groups := make(map[string]*storeGCGroup)
	for _, entry := range entries {
		clientID, recognized := storeFileClientID(entry.Name())
		if !recognized {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", entry.Name(), err)
		}
		group := groups[clientID]
		if group == nil {
			group = &storeGCGroup{clientID: clientID}
			groups[clientID] = group
		}
		group.names = append(group.names, entry.Name())
		if info.ModTime().After(group.newest) {
			group.newest = info.ModTime()
		}
	}

	var removed []string
	var result error
	for _, group := range groups {
		if keep[group.clientID] || time.Since(group.newest) < CollectGarbageAfter {
			continue
		}

		r.perStoreLock.Lock(group.clientID)
		r.mu.Lock()
		store := r.open[group.clientID]
		if store != nil && store.refCount > 0 {
			slog.Warn("skipping garbage collection of referenced client telemetry store", "clientID", group.clientID)
			r.mu.Unlock()
			r.perStoreLock.Unlock(group.clientID)
			continue
		}
		if store != nil {
			if idle := r.idleByID[group.clientID]; idle != nil {
				r.idle.Remove(idle)
				delete(r.idleByID, group.clientID)
			}
			delete(r.open, group.clientID)
			if err := store.closeStreams(); err != nil {
				result = errors.Join(result, fmt.Errorf("close telemetry store %s: %w", group.clientID, err))
				r.mu.Unlock()
				r.perStoreLock.Unlock(group.clientID)
				continue
			}
		}
		r.mu.Unlock()
		for _, name := range group.names {
			if err := os.RemoveAll(filepath.Join(r.Root, name)); err != nil {
				result = errors.Join(result, fmt.Errorf("remove %s: %w", name, err))
				continue
			}
			removed = append(removed, name)
		}
		r.perStoreLock.Unlock(group.clientID)
	}
	if len(removed) > 0 {
		slog.ExtraDebug("removed client telemetry stores", "files", removed)
	}
	return result
}

func storeFileClientID(name string) (string, bool) {
	for _, suffix := range []string{".spans.log", ".logs.log", ".metrics.log"} {
		if clientID, found := strings.CutSuffix(name, suffix); found && clientID != "" {
			return clientID, true
		}
	}
	if dbAt := strings.Index(name, ".db"); dbAt > 0 {
		suffix := name[dbAt+len(".db"):]
		if suffix == "" || suffix == "-wal" || suffix == "-shm" {
			return name[:dbAt], true
		}
	}
	return "", false
}
