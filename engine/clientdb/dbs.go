package clientdb

import (
	"database/sql"
	_ "embed"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DBs struct {
	Root string
}

func NewDBs(root string) *DBs {
	return &DBs{Root: root}
}

//go:embed schema.sql
var Schema string

func (dbs *DBs) Create(clientID string) (*sql.DB, error) {
	slog.Warn("!!! CREATE DB", "clientID", clientID)
	db, err := dbs.Open(clientID)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(Schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func (dbs *DBs) Open(clientID string) (*sql.DB, error) {
	slog.Warn("!!! OPEN DB", "clientID", clientID)
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
				"foreign_keys=ON",
				"journal_mode=WAL",
				"synchronous=NORMAL",
				"mmap_size=134217728",
				"journal_size_limit=27103364",
				"cache_size=2000",
			},
		}.Encode(),
		// ?cache=shared&mode=rwc&_busy_timeout=10000&_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys
	}
	db, err := sql.Open("sqlite", connURL.String())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", connURL, err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping %s: %w", connURL, err)
	}
	return db, nil
}

// TODO: not called by anything. GC based on time?
func (dbs *DBs) Remove(clientID string) error {
	slog.Warn("!!! REMOVE DB", "clientID", clientID)
	return os.RemoveAll(dbs.path(clientID))
}

func (dbs *DBs) path(clientID string) string {
	return filepath.Join(dbs.Root, clientID+".db")
}
