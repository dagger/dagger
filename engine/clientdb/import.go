package clientdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// importedTraces owns inspection-only stores. They never enter the client's
// export streams, and are discarded when its last telemetry handle closes.
//
// An imported trace can be far larger than anything else an engine holds for
// a session -- one Cloud trace's indexes measured ~930 MB -- and is read in
// bursts: a LoadTrace, then a few searches and reports. So imports are kept
// on disk in a registry of their own and opened like archived stores
// (DBs.OpenHeld): a reader holds one open, and once none has for the
// registry's idle hold, its indexes are released, to be rebuilt from disk by
// the next reader.
type importedTraces struct {
	mu      sync.Mutex
	reg     *DBs
	dir     string
	ready   map[string]string // trace ID -> the import's store in reg
	loading map[string]chan struct{}
	next    int
	closed  bool

	// idleHold and maxIdleHolds override the registry's defaults; tests
	// shorten them.
	idleHold     time.Duration
	maxIdleHolds int
}

// importRegistryLocked returns the imports' registry, creating it on first
// use. It requires s.imports.mu.
func (s *DB) importRegistryLocked() (*DBs, error) {
	if s.imports.closed {
		return nil, errStoreClosed
	}
	if s.imports.reg == nil {
		dir, err := os.MkdirTemp("", "dagger-trace-")
		if err != nil {
			return nil, err
		}
		reg := NewDBs(dir)
		if s.imports.idleHold != 0 {
			reg.IdleHold = s.imports.idleHold
		}
		if s.imports.maxIdleHolds != 0 {
			reg.MaxIdleHolds = s.imports.maxIdleHolds
		}
		s.imports.reg, s.imports.dir = reg, dir
	}
	return s.imports.reg, nil
}

// ImportTrace publishes a complete snapshot exactly once. Failed/canceled
// downloads remain invisible and may be retried. It returns a reference to
// the imported store, which the caller closes; s must stay retained while
// it is used.
func (s *DB) ImportTrace(ctx context.Context, id string, load func(*DB) error) (*DB, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.imports.mu.Lock()
		if key, ok := s.imports.ready[id]; ok {
			reg := s.imports.reg
			s.imports.mu.Unlock()
			return reg.OpenHeld(ctx, key)
		}
		if done := s.imports.loading[id]; done != nil {
			s.imports.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		reg, err := s.importRegistryLocked()
		if err != nil {
			s.imports.mu.Unlock()
			return nil, err
		}
		if s.imports.loading == nil {
			s.imports.loading = map[string]chan struct{}{}
		}
		done := make(chan struct{})
		s.imports.loading[id] = done
		// Store names are the registry's own: a trace ID is not a file name.
		key := fmt.Sprintf("import%d", s.imports.next)
		s.imports.next++
		s.imports.mu.Unlock()

		db, err := reg.Open(ctx, key)
		if err == nil {
			err = load(db)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			// Hold it open for its first readers.
			var held *DB
			if held, err = reg.OpenHeld(ctx, key); err == nil {
				err = held.Close()
			}
		}
		if err != nil {
			// Discard the partial capture: retired, it is deleted with its
			// last reference.
			err = errors.Join(err, reg.Retire(key))
			if db != nil {
				err = errors.Join(err, db.Close())
				db = nil
			}
		}
		s.imports.mu.Lock()
		if err == nil {
			if s.imports.ready == nil {
				s.imports.ready = map[string]string{}
			}
			s.imports.ready[id] = key
		}
		delete(s.imports.loading, id)
		close(done)
		s.imports.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return db, nil
	}
}

// InspectionStores returns the live store followed by imported snapshots in
// trace ID order, each import opened (and reindexed from disk, if it was
// released while idle) for the caller. Call release when done with them;
// retain s until then. Do not close the stores themselves.
func (s *DB) InspectionStores(ctx context.Context) (stores []*DB, release func(), err error) {
	s.imports.mu.Lock()
	ids := make([]string, 0, len(s.imports.ready))
	for id := range s.imports.ready {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = s.imports.ready[id]
	}
	reg := s.imports.reg
	s.imports.mu.Unlock()

	stores = []*DB{s}
	release = func() {
		for _, store := range stores[1:] {
			_ = store.Close()
		}
	}
	for _, key := range keys {
		store, err := reg.OpenHeld(ctx, key)
		if err != nil {
			release()
			return nil, nil, fmt.Errorf("open imported trace: %w", err)
		}
		stores = append(stores, store)
	}
	return stores, release, nil
}

// HasImports reports whether any trace has been imported for inspection.
func (s *DB) HasImports() bool {
	s.imports.mu.Lock()
	defer s.imports.mu.Unlock()
	return len(s.imports.ready) > 0
}

func (s *DB) closeImports() error {
	s.imports.mu.Lock()
	s.imports.closed = true
	reg, dir := s.imports.reg, s.imports.dir
	s.imports.mu.Unlock()
	if reg == nil {
		return nil
	}
	// Releases the idle holds; a reader still holding an import keeps its
	// open files, which outlive the directory.
	return errors.Join(reg.Close(), os.RemoveAll(dir))
}
