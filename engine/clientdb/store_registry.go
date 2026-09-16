package clientdb

import (
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

var errDBsClosed = errors.New("telemetry store registry is closed")

// DBs owns the refcounted set of open per-client telemetry stores. Live client
// runtimes and readers retain references; the final Close releases the streams.
type DBs struct {
	Root string

	open        map[string]*DB
	opening     int
	closed      bool
	mu          sync.RWMutex
	openingCond *sync.Cond

	perStoreLock *locker.Locker
	tailBudget   int64
	openStore    func(context.Context, string, string, int64) (*DB, error)
}

// OpenStats is a measured snapshot of currently open telemetry stores.
// Each referenced store owns exactly three stream handles.
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
		perStoreLock: locker.New(),
		tailBudget:   telemetryTailBudget,
		openStore:    openStore,
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
		store.refCount++
		r.mu.Unlock()
		return store, nil
	}
	r.opening++
	r.mu.Unlock()

	store, err := r.openStore(ctx, r.Root, clientID, r.tailBudget)

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

// close assumes no registry mutex is held. The per-client lock serializes
// reference changes and keeps a new writer from opening until the old one closes.
func (r *DBs) close(store *DB) error {
	r.perStoreLock.Lock(store.clientID)
	defer r.perStoreLock.Unlock(store.clientID)

	r.mu.Lock()
	if store.refCount <= 0 {
		r.mu.Unlock()
		return errStoreClosed
	}
	store.refCount--
	if store.refCount > 0 {
		r.mu.Unlock()
		return nil
	}
	delete(r.open, store.clientID)
	r.mu.Unlock()
	return store.closeStreams()
}

// Close prevents new stores from opening and waits for in-flight opens.
// Actively referenced stores remain usable and close their streams when their
// final handle is released.
func (r *DBs) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for r.opening > 0 {
		r.openingCond.Wait()
	}
	return nil
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
		if store != nil {
			slog.Warn("skipping garbage collection of referenced client telemetry store", "clientID", group.clientID)
			r.mu.Unlock()
			r.perStoreLock.Unlock(group.clientID)
			continue
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
