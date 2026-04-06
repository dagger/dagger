CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT, WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS results (
    id INTEGER PRIMARY KEY,
    call_frame_json TEXT NOT NULL,
    self_payload BLOB NOT NULL,
    output_effect_ids_json TEXT NOT NULL DEFAULT '[]',
    expires_at_unix INTEGER NOT NULL DEFAULT 0,
    created_at_unix_nano INTEGER NOT NULL,
    last_used_at_unix_nano INTEGER NOT NULL,
    record_type TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE IF NOT EXISTS eq_classes (
    id INTEGER PRIMARY KEY
) STRICT;

CREATE TABLE IF NOT EXISTS eq_class_digests (
    eq_class_id INTEGER NOT NULL,
    digest TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(eq_class_id, digest, label),
    FOREIGN KEY(eq_class_id) REFERENCES eq_classes(id) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS terms (
    id INTEGER PRIMARY KEY,
    self_digest TEXT NOT NULL,
    term_digest TEXT NOT NULL,
    output_eq_class_id INTEGER NOT NULL,
    FOREIGN KEY(output_eq_class_id) REFERENCES eq_classes(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE IF NOT EXISTS term_inputs (
    term_id INTEGER NOT NULL,
    position INTEGER NOT NULL,
    input_eq_class_id INTEGER NOT NULL,
    provenance_kind TEXT NOT NULL,
    PRIMARY KEY(term_id, position),
    FOREIGN KEY(term_id) REFERENCES terms(id) ON DELETE CASCADE,
    FOREIGN KEY(input_eq_class_id) REFERENCES eq_classes(id) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS result_output_eq_classes (
    result_id INTEGER NOT NULL,
    eq_class_id INTEGER NOT NULL,
    PRIMARY KEY(result_id, eq_class_id),
    FOREIGN KEY(result_id) REFERENCES results(id) ON DELETE CASCADE,
    FOREIGN KEY(eq_class_id) REFERENCES eq_classes(id) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS result_deps (
    parent_result_id INTEGER NOT NULL,
    dep_result_id INTEGER NOT NULL,
    PRIMARY KEY(parent_result_id, dep_result_id),
    FOREIGN KEY(parent_result_id) REFERENCES results(id) ON DELETE CASCADE,
    FOREIGN KEY(dep_result_id) REFERENCES results(id) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS persisted_edges (
    result_id INTEGER PRIMARY KEY,
    created_at_unix_nano INTEGER NOT NULL,
    expires_at_unix INTEGER NOT NULL DEFAULT 0,
    unpruneable INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY(result_id) REFERENCES results(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE IF NOT EXISTS result_snapshot_links (
    result_id INTEGER NOT NULL,
    ref_key TEXT NOT NULL,
    role TEXT NOT NULL,
    slot TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(result_id, ref_key, role, slot),
    FOREIGN KEY(result_id) REFERENCES results(id) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS snapshot_content_links (
    snapshot_id TEXT NOT NULL,
    digest TEXT NOT NULL,
    PRIMARY KEY(snapshot_id, digest)
) STRICT, WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS imported_layer_blob_index (
    parent_snapshot_id TEXT NOT NULL,
    blob_digest TEXT NOT NULL,
    snapshot_id TEXT NOT NULL,
    PRIMARY KEY(parent_snapshot_id, blob_digest)
) STRICT, WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS imported_layer_diff_index (
    parent_snapshot_id TEXT NOT NULL,
    diff_id TEXT NOT NULL,
    snapshot_id TEXT NOT NULL,
    PRIMARY KEY(parent_snapshot_id, diff_id)
) STRICT, WITHOUT ROWID;
