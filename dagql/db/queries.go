package db

import (
	"context"
)

const selectCall = `SELECT call_key, storage_key, expiration FROM calls WHERE call_key = ?`

func (q *Queries) SelectCall(ctx context.Context, key string) (*Call, error) {
	row := q.queryRow(ctx, q.selectCallStmt, selectCall, key)
	var i Call
	err := row.Scan(&i.CallKey, &i.StorageKey, &i.Expiration)
	return &i, err
}

// Upsert a new expiration only if we are only updating from the previous entry we read earlier;
// "compare-and-upsert" essentially.
const setExpiration = `
INSERT INTO calls (call_key, storage_key, expiration)
VALUES (?, ?, ?)
ON CONFLICT (call_key) DO UPDATE SET
	expiration = EXCLUDED.expiration,
	storage_key = EXCLUDED.storage_key
WHERE calls.storage_key = ?
`

type SetExpirationParams struct {
	CallKey        string
	StorageKey     string
	Expiration     int64
	PrevStorageKey string
}

func (q *Queries) SetExpiration(ctx context.Context, arg SetExpirationParams) error {
	_, err := q.exec(ctx, q.setExpirationStmt, setExpiration,
		arg.CallKey, arg.StorageKey, arg.Expiration, arg.PrevStorageKey,
	)
	return err
}

const gcBatchSize = 1000
const gcBatchSizeStr = "1000"

// Delete in batches to prevent hogging a write lock for too long.
// We don't currently have sqlite with "-DSQLITE_ENABLE_UPDATE_DELETE_LIMIT", so need a subquery right now
const gcExpiredCalls = `
DELETE FROM calls
WHERE call_key IN (
	SELECT call_key FROM calls
	WHERE expiration < ?
	LIMIT ` + gcBatchSizeStr + `
)`

type GCExpiredCallsParams struct {
	Now int64
}

func (q *Queries) GCExpiredCalls(ctx context.Context, arg GCExpiredCallsParams) error {
	for {
		result, err := q.exec(ctx, nil, gcExpiredCalls, arg.Now)
		if err != nil {
			return err
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rowsAffected < gcBatchSize {
			break
		}
	}
	return nil
}
