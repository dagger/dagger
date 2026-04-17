package persistdb

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed schema.sql
var Schema string

type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func New(db DBTX) *Queries {
	return &Queries{db: db}
}

func Prepare(ctx context.Context, db DBTX) (*Queries, error) {
	q := Queries{db: db}
	var err error
	if q.selectMetaStmt, err = db.PrepareContext(ctx, selectMeta); err != nil {
		return nil, fmt.Errorf("error preparing query SelectMeta: %w", err)
	}
	if q.upsertMetaStmt, err = db.PrepareContext(ctx, upsertMeta); err != nil {
		return nil, fmt.Errorf("error preparing query UpsertMeta: %w", err)
	}
	return &q, nil
}

func (q *Queries) Close() error {
	var err error
	if q.selectMetaStmt != nil {
		if cerr := q.selectMetaStmt.Close(); cerr != nil {
			err = fmt.Errorf("error closing selectMetaStmt: %w", cerr)
		}
	}
	if q.upsertMetaStmt != nil {
		if cerr := q.upsertMetaStmt.Close(); cerr != nil {
			err = fmt.Errorf("error closing upsertMetaStmt: %w", cerr)
		}
	}
	return err
}

func (q *Queries) exec(ctx context.Context, stmt *sql.Stmt, query string, args ...any) (sql.Result, error) {
	switch {
	case stmt != nil && q.tx != nil:
		return q.tx.StmtContext(ctx, stmt).ExecContext(ctx, args...)
	case stmt != nil:
		return stmt.ExecContext(ctx, args...)
	default:
		return q.db.ExecContext(ctx, query, args...)
	}
}

func (q *Queries) queryRow(ctx context.Context, stmt *sql.Stmt, query string, args ...any) *sql.Row {
	switch {
	case stmt != nil && q.tx != nil:
		return q.tx.StmtContext(ctx, stmt).QueryRowContext(ctx, args...)
	case stmt != nil:
		return stmt.QueryRowContext(ctx, args...)
	default:
		return q.db.QueryRowContext(ctx, query, args...)
	}
}

type Queries struct {
	db DBTX
	tx *sql.Tx

	selectMetaStmt *sql.Stmt
	upsertMetaStmt *sql.Stmt
}

func (q *Queries) WithTx(tx *sql.Tx) *Queries {
	return &Queries{
		db:             tx,
		tx:             tx,
		selectMetaStmt: q.selectMetaStmt,
		upsertMetaStmt: q.upsertMetaStmt,
	}
}
