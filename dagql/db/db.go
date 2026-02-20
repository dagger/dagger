package db

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
	if q.selectCallStmt, err = db.PrepareContext(ctx, selectCall); err != nil {
		return nil, fmt.Errorf("error preparing query SelectCall: %w", err)
	}
	if q.setExpirationStmt, err = db.PrepareContext(ctx, setExpiration); err != nil {
		return nil, fmt.Errorf("error preparing query SetExpiration: %w", err)
	}
	return &q, nil
}

func (q *Queries) Close() error {
	var err error
	if q.selectCallStmt != nil {
		if cerr := q.selectCallStmt.Close(); cerr != nil {
			err = fmt.Errorf("error closing selectCallStmt: %w", cerr)
		}
	}
	if q.setExpirationStmt != nil {
		if cerr := q.setExpirationStmt.Close(); cerr != nil {
			err = fmt.Errorf("error closing setExpirationStmt: %w", cerr)
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
	db                DBTX
	tx                *sql.Tx
	selectCallStmt    *sql.Stmt
	setExpirationStmt *sql.Stmt
}

func (q *Queries) WithTx(tx *sql.Tx) *Queries {
	return &Queries{
		db:                tx,
		tx:                tx,
		selectCallStmt:    q.selectCallStmt,
		setExpirationStmt: q.setExpirationStmt,
	}
}
