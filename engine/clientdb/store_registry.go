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

// StoreRegistry owns the refcounted set of open per-client telemetry stores.
// Stream files persist after the final Close and are recovered on the next
// Open, including their ID sequences and span lookup maps.
type StoreRegistry struct {
	Root string

	open map[string]*Store
	mu   sync.RWMutex

	perStoreLock *locker.Locker
	tailBudget   int64
}

func NewStoreRegistry(root string) *StoreRegistry {
	return &StoreRegistry{
		Root:         root,
		open:         make(map[string]*Store),
		perStoreLock: locker.New(),
		tailBudget:   telemetryTailBudget,
	}
}

func (r *StoreRegistry) Open(ctx context.Context, clientID string) (*Store, error) {
	r.perStoreLock.Lock(clientID)
	defer r.perStoreLock.Unlock(clientID)

	r.mu.Lock()
	if store := r.open[clientID]; store != nil {
		store.refCount++
		r.mu.Unlock()
		return store, nil
	}
	r.mu.Unlock()

	store, err := openStore(ctx, r.Root, clientID, r.tailBudget)
	if err != nil {
		return nil, err
	}
	store.refCount = 1
	store.closeFn = func() error {
		return r.close(store)
	}
	r.mu.Lock()
	r.open[clientID] = store
	r.mu.Unlock()
	return store, nil
}

// close assumes no registry mutex is held. The per-client lock covers the
// refcount and prevents a reopen from racing the final stream flush.
func (r *StoreRegistry) close(store *Store) error {
	r.perStoreLock.Lock(store.clientID)
	defer r.perStoreLock.Unlock(store.clientID)

	if store.refCount <= 0 {
		return errStoreClosed
	}
	store.refCount--
	if store.refCount > 0 {
		return nil
	}

	err := store.closeStreams()
	r.mu.Lock()
	delete(r.open, store.clientID)
	r.mu.Unlock()
	return err
}

type storeGCGroup struct {
	clientID string
	newest   time.Time
	names    []string
}

// GC removes complete client stores whose newest stream (or transitional
// SQLite sidecar) is older than CollectGarbageAfter. Grouping files by client
// keeps a recently active stream from being separated from an older sibling.
func (r *StoreRegistry) GC(keep map[string]bool) error {
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
		r.mu.RLock()
		_, open := r.open[group.clientID]
		r.mu.RUnlock()
		if open {
			slog.Warn("skipping garbage collection of client telemetry store that is still open", "clientID", group.clientID)
			r.perStoreLock.Unlock(group.clientID)
			continue
		}
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
