CREATE TABLE IF NOT EXISTS calls (
    key TEXT PRIMARY KEY,
    mixin TEXT NOT NULL,
    expiration INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS calls_exp_idx ON calls(expiration);
