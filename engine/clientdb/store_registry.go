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

var errStoreRetired = errors.New("telemetry store was retired")

// DBs owns the refcounted set of open per-client telemetry stores. Live client
// runtimes and readers retain references; the final Close releases the streams.
//
// A store whose last reference closes is reopened when telemetry for its
// client arrives late, for example from the session's shutdown flush after the
// client's runtime was reclaimed. DBs remembers where each closed store's
// streams ended, so such a reopen appends without scanning the store; see
// OpenForAppend.
type DBs struct {
	Root string

	open        map[string]*DB
	closedAt    map[string]*storePosition
	retired     map[string]time.Time // since when, until GC forgets it (see Retire)
	opening     int
	closed      bool
	mu          sync.RWMutex
	openingCond *sync.Cond

	perStoreLock *locker.Locker
	tailBudget   int64
	openStore    func(context.Context, string, string, int64, *storePosition) (*DB, error)
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
		closedAt:     make(map[string]*storePosition),
		retired:      make(map[string]time.Time),
		perStoreLock: locker.New(),
		tailBudget:   telemetryTailBudget,
		openStore:    openStoreStreams,
	}
	r.openingCond = sync.NewCond(&r.mu)
	return r
}

// Open returns a reference to the client's store, with its indexes built
// over every row it holds. Close the reference when done with it.
func (r *DBs) Open(ctx context.Context, clientID string) (*DB, error) {
	store, err := r.OpenForAppend(ctx, clientID)
	if err != nil {
		return nil, err
	}
	// Built outside the per-store lock: appends to the store go on while it
	// is built, and ensureIndexed catches up on them.
	if err := store.ensureIndexed(ctx); err != nil {
		return nil, errors.Join(err, store.Close())
	}
	return store, nil
}

// OpenForAppend returns a reference to the client's store for appending
// telemetry. Unlike Open it doesn't build the store's indexes, so reopening a
// closed store this way reads none of its rows; the selectors that answer
// from the indexes must not be used through it. Close the reference when done
// with it.
func (r *DBs) OpenForAppend(ctx context.Context, clientID string) (*DB, error) {
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
	if _, retired := r.retired[clientID]; retired {
		r.mu.Unlock()
		return nil, fmt.Errorf("open client %q: %w", clientID, errStoreRetired)
	}
	pos := r.closedAt[clientID]
	delete(r.closedAt, clientID)
	r.opening++
	r.mu.Unlock()

	store, err := r.openStore(ctx, r.Root, clientID, r.tailBudget, pos)

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
	if err := store.closeStreams(); err != nil {
		return err
	}
	r.mu.Lock()
	_, retired := r.retired[store.clientID]
	if !retired {
		r.closedAt[store.clientID] = store.position()
	}
	r.mu.Unlock()
	if retired {
		return r.removeStreams(store.clientID)
	}
	return nil
}

// Retire deletes a client's store once nothing can read it any more: now if
// nothing holds it, or when its last reference closes. From then on the
// store can't be reopened, so a late writer can't recreate it. A session
// retires its clients' stores when it ends.
func (r *DBs) Retire(clientID string) error {
	r.perStoreLock.Lock(clientID)
	defer r.perStoreLock.Unlock(clientID)
	r.mu.Lock()
	r.retired[clientID] = time.Now()
	delete(r.closedAt, clientID)
	used := r.open[clientID] != nil
	r.mu.Unlock()
	if used {
		return nil
	}
	return r.removeStreams(clientID)
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
	r.mu.Lock()
	for clientID, at := range r.retired {
		if time.Since(at) >= CollectGarbageAfter {
			delete(r.retired, clientID)
		}
	}
	r.mu.Unlock()
	return r.collect(keep, CollectGarbageAfter)
}

// Sweep removes every client store not in keep, however recent. An engine
// sweeps when it starts: its previous run's sessions and forwarders are gone,
// so nothing but the archives in keep can read those stores.
func (r *DBs) Sweep(keep map[string]bool) error {
	return r.collect(keep, 0)
}

// collect removes the unreferenced client stores not in keep whose newest
// stream is at least minAge old.
func (r *DBs) collect(keep map[string]bool, minAge time.Duration) error {
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
		if keep[group.clientID] || time.Since(group.newest) < minAge {
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
		delete(r.closedAt, group.clientID)
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

// Remove deletes a store only when no live writer or reader holds a reference.
// The same per-store lock serializes reopening, so GC cannot unlink a newly
// reopened archive stream.
func (r *DBs) Remove(clientID string) (bool, error) {
	r.perStoreLock.Lock(clientID)
	defer r.perStoreLock.Unlock(clientID)
	r.mu.Lock()
	used := r.open[clientID] != nil
	if !used {
		delete(r.closedAt, clientID)
	}
	r.mu.Unlock()
	if used {
		return false, nil
	}
	err := r.removeStreams(clientID)
	return err == nil, err
}

// removeStreams deletes a client's stream files. The caller holds the
// client's per-store lock and has made sure nothing has the store open.
func (r *DBs) removeStreams(clientID string) error {
	var result error
	for _, suffix := range storeStreamSuffixes {
		err := os.Remove(filepath.Join(r.Root, clientID+suffix))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

// StoreSize is the on-disk size of a client's store streams, measured without
// opening (and replaying) the store. Missing streams count as empty.
func (r *DBs) StoreSize(clientID string) (int64, error) {
	var total int64
	for _, suffix := range storeStreamSuffixes {
		info, err := os.Stat(filepath.Join(r.Root, clientID+suffix))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		total += info.Size()
	}
	return total, nil
}

var storeStreamSuffixes = []string{".spans.log", ".logs.log", ".metrics.log"}

func storeFileClientID(name string) (string, bool) {
	for _, suffix := range storeStreamSuffixes {
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
