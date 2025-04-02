CREATE TABLE IF NOT EXISTS calls (
  meta_digest TEXT NOT NULL,
  input_index INTEGER NOT NULL,
  input_result INTEGER,
  result_id INTEGER NOT NULL,
  FOREIGN KEY (input_result) REFERENCES results(result_id),
  FOREIGN KEY (result_id) REFERENCES results(result_id)
) STRICT;

CREATE TABLE IF NOT EXISTS results (
  result_id INTEGER PRIMARY KEY AUTOINCREMENT,
  json BLOB NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS resultDigests (
  result_id INTEGER NOT NULL,
  digest TEXT NOT NULL,
  PRIMARY KEY (result_id, digest),
  FOREIGN KEY (result_id) REFERENCES results(result_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_calls ON calls (meta_digest, input_index, input_result, result_id);
