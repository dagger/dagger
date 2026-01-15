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

// Open open a database for the given clientID if not open already and
// runs the schema migration if needed.
func (dbs *DBs) Open(ctx context.Context, clientID string) (_ *DB, rerr error) {
	lg := slog.Default().With("clientID", clientID)

	dbs.perDBLock.Lock(clientID)
	defer dbs.perDBLock.Unlock(clientID)

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

	if db.inner == nil {
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
		sqlDB, err := sql.Open("sqlite", connURL.String())
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", connURL, err)
		}
		if err := sqlDB.Ping(); err != nil {
			return nil, fmt.Errorf("ping %s: %w", connURL, err)
		}

		db.inner = sqlDB

		if !alreadyExists {
			if _, err := db.inner.Exec(Schema); err != nil {
				return nil, fmt.Errorf("migrate: %w", err)
			}
		}
	} else {
		lg.Trace("reusing open client DB", "clientID", clientID)
	}

	if db.Queries == nil {
		var err error
		db.Queries, err = Prepare(ctx, db.inner)
		if err != nil {
			return nil, fmt.Errorf("prepare queries: %w", err)
		}
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

	if db.Queries != nil {
		if cerr := db.Queries.Close(); cerr != nil {
			rerr = errors.Join(rerr, fmt.Errorf("error closing queries: %w", cerr))
		}
		db.Queries = nil
	}
	if db.inner != nil {
		if cerr := db.inner.Close(); cerr != nil {
			rerr = errors.Join(rerr, fmt.Errorf("error closing db: %w", cerr))
		}
		db.inner = nil
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

	inner *sql.DB
	*Queries

	refCount int
}

func (db *DB) Begin() (*sql.Tx, error) {
	return db.inner.Begin()
}

func (db *DB) Close() (rerr error) {
	lg := slog.Default().With("clientID", db.clientID)
	db.dbs.perDBLock.Lock(db.clientID)
	defer db.dbs.perDBLock.Unlock(db.clientID)
	return db.dbs.close(db, lg)
}
