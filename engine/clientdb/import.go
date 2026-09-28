package clientdb

import (
	"context"
	"errors"
	"os"
	"sort"
	"sync"
)

// importedTraces owns inspection-only stores. They never enter the client's
// export streams, and are discarded when its last telemetry handle closes.
type importedTraces struct {
	mu      sync.Mutex
	ready   map[string]*DB
	loading map[string]chan struct{}
	dirs    []string
}

// ImportTrace publishes a complete snapshot exactly once. Failed/canceled
// downloads remain invisible and may be retried. The caller must retain s
// while using the returned store, and must not close the returned store.
func (s *DB) ImportTrace(ctx context.Context, id string, load func(*DB) error) (*DB, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.imports.mu.Lock()
		if db := s.imports.ready[id]; db != nil {
			s.imports.mu.Unlock()
			return db, nil
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
		if s.imports.loading == nil {
			s.imports.loading = map[string]chan struct{}{}
		}
		done := make(chan struct{})
		s.imports.loading[id] = done
		s.imports.mu.Unlock()

		dir, err := os.MkdirTemp("", "dagger-trace-")
		var db *DB
		if err == nil {
			db, err = openStore(ctx, dir, "import", telemetryTailBudget)
		}
		if err == nil {
			err = load(db)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			if db != nil {
				err = errors.Join(err, db.Close())
			}
			if dir != "" {
				err = errors.Join(err, os.RemoveAll(dir))
			}
		}
		s.imports.mu.Lock()
		if err == nil {
			if s.imports.ready == nil {
				s.imports.ready = map[string]*DB{}
			}
			s.imports.ready[id] = db
			s.imports.dirs = append(s.imports.dirs, dir)
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
// trace ID order. Retain s while using them; do not close the borrowed stores.
func (s *DB) InspectionStores() []*DB {
	s.imports.mu.Lock()
	defer s.imports.mu.Unlock()
	ids := make([]string, 0, len(s.imports.ready))
	for id := range s.imports.ready {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	stores := []*DB{s}
	for _, id := range ids {
		stores = append(stores, s.imports.ready[id])
	}
	return stores
}

func (s *DB) closeImports() error {
	var err error
	for _, db := range s.imports.ready {
		err = errors.Join(err, db.Close())
	}
	for _, dir := range s.imports.dirs {
		err = errors.Join(err, os.RemoveAll(dir))
	}
	return err
}
