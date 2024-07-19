package clientdb

import (
	"database/sql"
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

func (dbs *DBs) Open(clientID string) (*sql.DB, error) {
	return sql.Open("sqlite", dbs.path(clientID))
}

// TODO: not called by anything
func (dbs *DBs) Remove(clientID string) error {
	return os.RemoveAll(dbs.path(clientID))
}

func (dbs *DBs) path(clientID string) string {
	return filepath.Join(dbs.Root, clientID+".db")
}
