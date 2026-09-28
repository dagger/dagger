package persistdb

import (
	"context"
	"database/sql"
	"errors"
)

const (
	MetaKeySchemaVersion = "schema_version"
	MetaKeyCleanShutdown = "clean_shutdown"
	// MetaKeyCacheID is the cache's identity, a random ID created with the
	// database; MetaKeyGeneration counts the cache's opens, the current one
	// included.
	MetaKeyCacheID    = "cache_id"
	MetaKeyGeneration = "generation"
	// MetaKeyNextResultID is the next result number the cache allocates,
	// saved with its state so a restored cache never reuses a number.
	MetaKeyNextResultID = "next_result_id"
)

const selectMeta = `SELECT key, value FROM meta WHERE key = ?`

func (q *Queries) SelectMeta(ctx context.Context, key string) (*Meta, error) {
	row := q.queryRow(ctx, q.selectMetaStmt, selectMeta, key)
	var m Meta
	err := row.Scan(&m.Key, &m.Value)
	return &m, err
}

const upsertMeta = `
INSERT INTO meta (key, value)
VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET
	value = EXCLUDED.value
`

func (q *Queries) UpsertMeta(ctx context.Context, key, value string) error {
	_, err := q.exec(ctx, q.upsertMetaStmt, upsertMeta, key, value)
	return err
}

func (q *Queries) SelectMetaValue(ctx context.Context, key string) (string, bool, error) {
	m, err := q.SelectMeta(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return m.Value, true, nil
}
