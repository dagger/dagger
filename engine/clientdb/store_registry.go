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

// Defaults for OpenHeld: how long a reader-only store stays open after its
// last use, and how many such stores may be held at once.
const (
	DefaultIdleHold     = 90 * time.Second
	DefaultMaxIdleHolds = 8
)

var errDBsClosed = errors.New("telemetry store registry is closed")

// DBs owns the refcounted set of open per-client telemetry stores. Live client
// runtimes and readers retain references; the final Close releases the streams.
//
// Readers of ended sessions (archive requests) open stores with OpenHeld,
// which keeps the store open for an idle period after use, so that a burst of
// requests does not replay the whole store from disk per request. Such an
// idle hold is a reference of its own, but it never blocks Remove, GC or
// Close: each of those releases the hold first.
type DBs struct {
	Root string
	// IdleHold and MaxIdleHolds configure OpenHeld. Set them before use.
	IdleHold     time.Duration
	MaxIdleHolds int

	open        map[string]*DB
	held        map[string]*idleHold
	opening     int
	closed      bool
	mu          sync.RWMutex
	openingCond *sync.Cond

	perStoreLock *locker.Locker
	tailBudget   int64
	openStore    func(context.Context, string, string, int64) (*DB, error)
}

// idleHold is the reference OpenHeld keeps on a store until it has been idle
// for the registry's IdleHold. Its fields are guarded by DBs.mu.
type idleHold struct {
	store    *DB
	deadline time.Time
	timer    *time.Timer
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
		IdleHold:     DefaultIdleHold,
		MaxIdleHolds: DefaultMaxIdleHolds,
		open:         make(map[string]*DB),
		held:         make(map[string]*idleHold),
		perStoreLock: locker.New(),
		tailBudget:   telemetryTailBudget,
		openStore:    openStore,
	}
	r.openingCond = sync.NewCond(&r.mu)
	return r
}

func (r *DBs) Open(ctx context.Context, clientID string) (*DB, error) {
	return r.openStoreRef(ctx, clientID, false)
}

// OpenHeld opens clientID like Open, and additionally keeps the store open
// until it has gone unused for IdleHold; every OpenHeld refreshes that
// deadline. Use it for readers of stores no live session holds, such as
// archive requests, which otherwise reopen (and replay) the store per
// request. At most MaxIdleHolds stores are held; the least recently used hold
// beyond that is released. The caller still closes the returned handle.
func (r *DBs) OpenHeld(ctx context.Context, clientID string) (*DB, error) {
	store, err := r.openStoreRef(ctx, clientID, true)
	if err != nil {
		return nil, err
	}
	r.evictIdleHolds()
	return store, nil
}

func (r *DBs) openStoreRef(ctx context.Context, clientID string, hold bool) (*DB, error) {
	r.perStoreLock.Lock(clientID)
	defer r.perStoreLock.Unlock(clientID)

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errDBsClosed
	}
	if store := r.open[clientID]; store != nil {
		store.refCount++
		if hold {
			r.holdLocked(clientID, store)
		}
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
	if hold {
		r.holdLocked(clientID, store)
	}
	r.mu.Unlock()
	return store, nil
}

// holdLocked takes or refreshes clientID's idle hold on store. It requires
// r.mu and clientID's per-store lock, with store open and referenced.
func (r *DBs) holdLocked(clientID string, store *DB) {
	if r.IdleHold <= 0 || r.MaxIdleHolds <= 0 {
		return
	}
	deadline := time.Now().Add(r.IdleHold)
	if h := r.held[clientID]; h != nil {
		// While a hold exists its store stays open, so it is this store.
		h.deadline = deadline
		return
	}
	h := &idleHold{store: store, deadline: deadline}
	store.refCount++
	r.held[clientID] = h
	h.timer = time.AfterFunc(r.IdleHold, func() { r.expireIdleHold(clientID, h) })
}

// expireIdleHold releases h once its deadline has passed, or re-arms its timer
// for a deadline refreshed since it was set. A store still in use by other
// handles is not idle: its hold is extended, so a request outliving IdleHold
// does not close the store (and drop its caches) when it finishes.
func (r *DBs) expireIdleHold(clientID string, h *idleHold) {
	r.mu.Lock()
	if r.held[clientID] != h {
		r.mu.Unlock()
		return
	}
	if h.store.refCount > 1 {
		h.deadline = time.Now().Add(r.IdleHold)
	}
	if remaining := time.Until(h.deadline); remaining > 0 {
		h.timer.Reset(remaining)
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	r.releaseIdleHold(clientID, h)
}

// releaseIdleHold drops h's reference, if h is still clientID's hold. It
// takes clientID's per-store lock, so the caller must not hold it.
func (r *DBs) releaseIdleHold(clientID string, h *idleHold) {
	r.perStoreLock.Lock(clientID)
	defer r.perStoreLock.Unlock(clientID)
	if err := r.releaseIdleHoldLocked(clientID, h); err != nil {
		slog.Warn("closing idle telemetry store", "clientID", clientID, "err", err)
	}
}

// releaseIdleHoldLocked drops clientID's idle hold (h, or whichever hold it
// has when h is nil), closing the store if that was its last reference. It
// requires clientID's per-store lock and not r.mu.
func (r *DBs) releaseIdleHoldLocked(clientID string, h *idleHold) error {
	r.mu.Lock()
	current := r.held[clientID]
	if current == nil || (h != nil && current != h) {
		r.mu.Unlock()
		return nil
	}
	delete(r.held, clientID)
	current.timer.Stop()
	r.mu.Unlock()
	return r.closeLocked(current.store)
}

// evictIdleHolds releases the least recently used holds beyond MaxIdleHolds.
func (r *DBs) evictIdleHolds() {
	for {
		r.mu.Lock()
		if len(r.held) <= max(r.MaxIdleHolds, 0) {
			r.mu.Unlock()
			return
		}
		var victimID string
		var victim *idleHold
		for clientID, h := range r.held {
			if victim == nil || h.deadline.Before(victim.deadline) {
				victimID, victim = clientID, h
			}
		}
		r.mu.Unlock()
		r.releaseIdleHold(victimID, victim)
	}
}

// close assumes no registry mutex is held. The per-client lock serializes
// reference changes and keeps a new writer from opening until the old one closes.
func (r *DBs) close(store *DB) error {
	r.perStoreLock.Lock(store.clientID)
	defer r.perStoreLock.Unlock(store.clientID)
	return r.closeLocked(store)
}

// closeLocked drops one reference to store. It requires store's per-store
// lock and not r.mu.
func (r *DBs) closeLocked(store *DB) error {
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

// Close prevents new stores from opening, waits for in-flight opens, and
// releases idle holds. Actively referenced stores remain usable and close
// their streams when their final handle is released.
func (r *DBs) Close() error {
	r.mu.Lock()
	r.closed = true
	for r.opening > 0 {
		r.openingCond.Wait()
	}
	held := r.held
	r.held = make(map[string]*idleHold)
	for _, h := range held {
		h.timer.Stop()
	}
	r.mu.Unlock()
	var result error
	for clientID, h := range held {
		r.perStoreLock.Lock(clientID)
		result = errors.Join(result, r.closeLocked(h.store))
		r.perStoreLock.Unlock(clientID)
	}
	return result
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
		// An idle hold never keeps an otherwise collectable store.
		if err := r.releaseIdleHoldLocked(group.clientID, nil); err != nil {
			result = errors.Join(result, fmt.Errorf("close idle store %s: %w", group.clientID, err))
		}
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

// Remove deletes a store only when no live writer or reader holds a reference.
// An idle hold (see OpenHeld) is released first rather than blocking removal.
// The same per-store lock serializes reopening, so GC cannot unlink a newly
// reopened archive stream.
func (r *DBs) Remove(clientID string) (bool, error) {
	r.perStoreLock.Lock(clientID)
	defer r.perStoreLock.Unlock(clientID)
	if err := r.releaseIdleHoldLocked(clientID, nil); err != nil {
		return false, err
	}
	r.mu.RLock()
	used := r.open[clientID] != nil
	r.mu.RUnlock()
	if used {
		return false, nil
	}
	var result error
	for _, suffix := range storeStreamSuffixes {
		err := os.Remove(filepath.Join(r.Root, clientID+suffix))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result == nil, result
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
