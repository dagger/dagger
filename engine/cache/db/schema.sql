CREATE TABLE IF NOT EXISTS calls (
    call_key TEXT PRIMARY KEY,
    expiration INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS calls_exp_idx ON calls(expiration);
