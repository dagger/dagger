package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(dbPath string) (*Store, error) {
	dbPath, err := filepath.Abs(filepath.Clean(dbPath))
	if err != nil {
		return nil, fmt.Errorf("resolve db path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	uriPath := filepath.ToSlash(dbPath)
	if !strings.HasPrefix(uriPath, "/") {
		// file URIs must use an absolute path component (e.g. /C:/foo on Windows).
		uriPath = "/" + uriPath
	}

	connURL := &url.URL{
		Scheme: "file",
		Path:   uriPath,
	}
	q := connURL.Query()
	q.Add("_pragma", "journal_mode=WAL")
	q.Add("_pragma", "busy_timeout=5000")
	q.Add("_pragma", "foreign_keys=ON")
	connURL.RawQuery = q.Encode()

	db, err := sql.Open("sqlite", connURL.String())
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	st := &Store{db: db}
	if err := st.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return st, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) init(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS traces (
  trace_id TEXT PRIMARY KEY,
  source_mode TEXT NOT NULL DEFAULT 'collector',
  first_seen_unix_nano INTEGER NOT NULL,
  last_seen_unix_nano INTEGER NOT NULL,
  span_count INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'unknown'
);

CREATE TABLE IF NOT EXISTS spans (
  trace_id TEXT NOT NULL,
  span_id TEXT NOT NULL,
  parent_span_id TEXT,
  name TEXT NOT NULL,
  start_unix_nano INTEGER NOT NULL,
  end_unix_nano INTEGER NOT NULL,
  status_code TEXT NOT NULL DEFAULT '',
  status_message TEXT NOT NULL DEFAULT '',
  updated_unix_nano INTEGER NOT NULL,
  data_json TEXT NOT NULL,
  PRIMARY KEY(trace_id, span_id)
);

CREATE INDEX IF NOT EXISTS idx_spans_trace_start ON spans(trace_id, start_unix_nano);
`
	_, err := s.db.ExecContext(ctx, schema)
	if err != nil {
		return fmt.Errorf("init schema: %w", err)
	}
	return nil
}
