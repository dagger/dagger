package clientdb

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagger/dagger/engine/slog"
	_ "modernc.org/sqlite"
)

type DBs struct {
	Root string
}

// CollectGarbageAfter is the time after which a database is considered
// garbage and can be deleted.
const CollectGarbageAfter = time.Hour

func NewDBs(root string) *DBs {
	return &DBs{Root: root}
}

//go:embed schema.sql
var Schema string

// Create creates a new database for the given clientID and runs the schema
// migration. This operation must be idempotent.
func (dbs *DBs) Create(clientID string) (*sql.DB, error) {
	db, err := dbs.Open(clientID)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(Schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

// Open opens the database for the given client, for reading.
func (dbs *DBs) Open(clientID string) (*sql.DB, error) {
	dbPath := dbs.path(clientID)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(dbPath), err)
	}
	connURL := &url.URL{
		Scheme: "file",
		Host:   "",
		Path:   dbPath,
		RawQuery: url.Values{
			"_pragma": []string{
				"foreign_keys=ON",    // we don't use em yet, but makes sense anyway
				"journal_mode=WAL",   // readers don't block writers and vice versa
				"synchronous=OFF",    // we don't care about durability and don't want to be surprised by syncs
				"busy_timeout=20000", // wait up to 20s when there are concurrent writers
			},
			"_txlock": []string{"immediate"}, // use BEGIN IMMEDIATE for transactions
		}.Encode(),
	}
	db, err := sql.Open("sqlite", connURL.String())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", connURL, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s: %w", connURL, err)
	}
	return db, nil
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
