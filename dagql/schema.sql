CREATE TABLE IF NOT EXISTS calls (
  meta_digest TEXT NOT NULL,
  input_index INTEGER NOT NULL,
  input_result TEXT,
  result_id TEXT NOT NULL,
  FOREIGN KEY (input_result) REFERENCES results(result_id),
  FOREIGN KEY (result_id) REFERENCES results(result_id)
) STRICT;

CREATE TABLE IF NOT EXISTS results (
  result_id TEXT PRIMARY KEY,
  json BLOB NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS resultDigests (
  result_id TEXT NOT NULL,
  digest TEXT NOT NULL,
  PRIMARY KEY (result_id, digest),
  FOREIGN KEY (result_id) REFERENCES results(result_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_calls ON calls (meta_digest, input_index, input_result, result_id);
