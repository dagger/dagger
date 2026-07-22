package clientdb

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/slog"
	"github.com/moby/locker"
	_ "modernc.org/sqlite"
)

type DBs struct {
	Root string

	open map[string]*DB
	mu   sync.RWMutex // mutex just for reading writing map

	perDBLock *locker.Locker // mutex for each DB
}

// CollectGarbageAfter is the time after which a database is considered
// garbage and can be deleted.
const CollectGarbageAfter = time.Hour

func NewDBs(root string) *DBs {
	return &DBs{
		Root:      root,
		open:      make(map[string]*DB),
		perDBLock: locker.New(),
	}
}

//go:embed schema.sql
var Schema string

// slowOpen flags Open calls that spent significant time waiting on the
// per-DB lock (or on the initial SQLite open): every telemetry export batch
// re-opens the DB by refcount, so contention here serializes exporters
// before they even reach the connection pool.
const slowOpen = 1 * time.Second

// Each client DB uses two connection pools against the same file. SQLite
// permits exactly one writer, so the write pool is capped at a single
// connection: extra write connections can't write in parallel, they only lose
// the BEGIN IMMEDIATE race and busy-wait for the write lock up to busy_timeout
// (10s, == the client shutdown budget — a cap of 10 was tried and did exactly
// that, timing out shutdowns). One write connection serializes writers cheaply
// in database/sql's queue with no SQLite-level busy-waiting. Reads get their
// own pool so that, under WAL, SSE drains and log reads run concurrently with
// the writer instead of queuing behind it on a shared connection.
const (
	writeMaxConns = 1
	readMaxConns  = 4
)

// Open open a database for the given clientID if not open already and
// runs the schema migration if needed.
func (dbs *DBs) Open(ctx context.Context, clientID string) (_ *DB, rerr error) {
	lg := slog.Default().With("clientID", clientID)

	openStart := time.Now()
	dbs.perDBLock.Lock(clientID)
	defer dbs.perDBLock.Unlock(clientID)
	if waited := time.Since(openStart); waited > slowOpen {
		lg.Warn("slow client DB open", "waited", waited)
	}

	dbs.mu.Lock()
	db, ok := dbs.open[clientID]
	if !ok {
		db = &DB{
			dbs:      dbs,
			clientID: clientID,
		}
		dbs.open[clientID] = db
	}
	dbs.mu.Unlock()

	db.refCount++ // increment now to handle case of context cancelled before we return
	lg = lg.With("aquiredRefCount", db.refCount)
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, dbs.close(db, lg))
		}
	}()

	if db.writer == nil {
		lg.ExtraDebug("opening client DB", "clientID", clientID)

		dbPath := db.dbs.path(db.clientID)
		if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(dbPath), err)
		}

		// check whether the file exists already
		_, statErr := os.Lstat(dbPath)
		alreadyExists := statErr == nil

		connURL := &url.URL{
			Scheme: "file",
			Host:   "",
			Path:   dbPath,
			RawQuery: url.Values{
				"_pragma": []string{
					"foreign_keys=ON",    // we don't use em yet, but makes sense anyway
					"journal_mode=WAL",   // readers don't block writers and vice versa
					"synchronous=OFF",    // we don't care about durability and don't want to be surprised by syncs
					"busy_timeout=10000", // wait up to 10s when there are concurrent writers
				},
				"_txlock": []string{"immediate"}, // use BEGIN IMMEDIATE for transactions
			}.Encode(),
		}
		dsn := connURL.String()

		// Writer pool: a single connection (see writeMaxConns). The file is
		// created and migrated here, before the reader pool opens against it.
		writer, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, fmt.Errorf("open writer %s: %w", connURL, err)
		}
		writer.SetMaxOpenConns(writeMaxConns)
		if err := writer.Ping(); err != nil {
			return nil, fmt.Errorf("ping writer %s: %w", connURL, err)
		}
		db.writer = writer

		if !alreadyExists {
			if _, err := db.writer.Exec(Schema); err != nil {
				return nil, fmt.Errorf("migrate: %w", err)
			}
		}

		// Reader pool: separate connections so WAL reads (SSE drains, log
		// reads) run concurrently with the single writer instead of queuing
		// behind it. Read-only by convention — only Select* is routed here (via
		// DB.Read) — so these connections never take the write lock.
		reader, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, fmt.Errorf("open reader %s: %w", connURL, err)
		}
		reader.SetMaxOpenConns(readMaxConns)
		if err := reader.Ping(); err != nil {
			return nil, fmt.Errorf("ping reader %s: %w", connURL, err)
		}
		db.reader = reader
	} else {
		lg.Trace("reusing open client DB", "clientID", clientID)
	}

	if db.writeQueries == nil {
		var err error
		db.writeQueries, err = Prepare(ctx, db.writer)
		if err != nil {
			return nil, fmt.Errorf("prepare write queries: %w", err)
		}
	}
	if db.readQueries == nil {
		var err error
		db.readQueries, err = Prepare(ctx, db.reader)
		if err != nil {
			return nil, fmt.Errorf("prepare read queries: %w", err)
		}
	}
	if db.writeAgent == nil {
		db.writeAgent = newWriteAgent(db.writer, db.writeQueries)
	}

	return db, nil
}

// assumes dbs.perDBLock is held for clientID and dbs.mu is *not* held
func (dbs *DBs) close(db *DB, lg *slog.Logger) (rerr error) {
	db.refCount--
	lg = lg.With("releasedRefCount", db.refCount)

	if db.refCount > 0 {
		lg.Trace("not closing client DB; still has references")
		return nil
	}
	lg.ExtraDebug("closing client DB; no more references")

	if db.writeAgent != nil {
		db.writeAgent.Close()
	}
	if db.writeQueries != nil {
		if cerr := db.writeQueries.Close(); cerr != nil {
			rerr = errors.Join(rerr, fmt.Errorf("error closing write queries: %w", cerr))
		}
		db.writeQueries = nil
	}
	if db.readQueries != nil {
		if cerr := db.readQueries.Close(); cerr != nil {
			rerr = errors.Join(rerr, fmt.Errorf("error closing read queries: %w", cerr))
		}
		db.readQueries = nil
	}
	if db.writer != nil {
		if cerr := db.writer.Close(); cerr != nil {
			rerr = errors.Join(rerr, fmt.Errorf("error closing writer: %w", cerr))
		}
		db.writer = nil
	}
	if db.reader != nil {
		if cerr := db.reader.Close(); cerr != nil {
			rerr = errors.Join(rerr, fmt.Errorf("error closing reader: %w", cerr))
		}
		db.reader = nil
	}

	dbs.mu.Lock()
	defer dbs.mu.Unlock()
	delete(dbs.open, db.clientID)

	return rerr
}

// GC removes databases that are older than CollectGarbageAfter based on mtime.
func (dbs *DBs) GC(keep map[string]bool) error {
	ents, err := os.ReadDir(dbs.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// no databases found
			return nil
		}
		return fmt.Errorf("readdir %s: %w", dbs.Root, err)
	}
	var removed []string
	var errs error
	for _, ent := range ents {
		info, err := ent.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", ent.Name(), err)
		}
		clientID, _, ok := strings.Cut(ent.Name(), ".")
		if !ok {
			continue
		}
		if keep[clientID] {
			// client still active; keep it around
			continue
		}
		if time.Since(info.ModTime()) < CollectGarbageAfter {
			// DB is still fresh; keep
			continue
		}
		dbs.mu.RLock()
		_, openBySomeone := dbs.open[clientID]
		dbs.mu.RUnlock()
		if openBySomeone {
			// DB is still open by someone; keep it but log this since this is a weird case, possibly indicative of a leak
			slog.Warn("skipping garbage collection of client DB that is still open", "clientID", clientID)
			continue
		}
		if err := os.RemoveAll(filepath.Join(dbs.Root, ent.Name())); err != nil {
			errs = errors.Join(errs, fmt.Errorf("remove %s: %w", ent.Name(), err))
		}
		removed = append(removed, ent.Name())
	}
	if len(removed) > 0 {
		slog.ExtraDebug("removed client DBs", "clients", removed)
	}
	return errs
}

func (dbs *DBs) path(clientID string) string {
	return filepath.Join(dbs.Root, clientID+".db")
}

type DB struct {
	dbs      *DBs
	clientID string

	// writer is the single-connection write pool. Only writeAgent acquires it;
	// writeQueries is private so callers cannot bypass the fan-in queue.
	writer       *sql.DB
	writeQueries *Queries
	writeAgent   *writeAgent

	// reader is the multi-connection read pool; readQueries is prepared
	// against it and reached via Read(), so Select* reads run concurrently
	// with the writer under WAL.
	reader      *sql.DB
	readQueries *Queries

	refCount int
}

// Read returns the queries bound to the read pool. Route all Select* reads
// (SSE drains, log reads) through this so they use the reader connections and
// never contend with writes on the single write connection.
func (db *DB) Read() *Queries {
	return db.readQueries
}

// Write queues one telemetry batch on this DB's sole writer and blocks until
// the coalesced transaction containing it commits. This guarantees that a nil
// return means the batch is already visible through Read().
func (db *DB) Write(ctx context.Context, write WriteBatch) (WriteTiming, error) {
	return db.writeAgent.submit(ctx, write)
}

// Stats reports the write pool's counters. It is capped at a single connection
// and only acquired by the write agent, so WaitCount/WaitDuration should stay
// near zero (reads use the separate reader pool and do not show up here).
func (db *DB) Stats() sql.DBStats {
	return db.writer.Stats()
}

func (db *DB) Close() (rerr error) {
	if db == nil {
		return nil
	}
	lg := slog.Default().With("clientID", db.clientID)
	db.dbs.perDBLock.Lock(db.clientID)
	defer db.dbs.perDBLock.Unlock(db.clientID)
	return db.dbs.close(db, lg)
}
