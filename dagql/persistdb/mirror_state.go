package persistdb

import "context"

type MirrorResult struct {
	ID                 int64
	CallFrameJSON      string
	SelfPayload        []byte
	OutputEffectIDs    string
	ExpiresAtUnix      int64
	CreatedAtUnixNano  int64
	LastUsedAtUnixNano int64
	RecordType         string
	Description        string
}

type MirrorEqClass struct {
	ID int64
}

type MirrorEqClassDigest struct {
	EqClassID int64
	Digest    string
	Label     string
}

type MirrorTerm struct {
	ID              int64
	SelfDigest      string
	TermDigest      string
	OutputEqClassID int64
}

type MirrorTermInput struct {
	TermID         int64
	Position       int64
	InputEqClassID int64
	ProvenanceKind string
}

type MirrorResultOutputEqClass struct {
	ResultID  int64
	EqClassID int64
}

type MirrorResultDep struct {
	ParentResultID int64
	DepResultID    int64
}

type MirrorPersistedEdge struct {
	ResultID          int64
	CreatedAtUnixNano int64
	ExpiresAtUnix     int64
	Unpruneable       bool
}

type MirrorResultSnapshotLink struct {
	ResultID int64
	RefKey   string
	Role     string
}

type MirrorSnapshotContentLink struct {
	SnapshotID string
	Digest     string
}

type MirrorImportedLayerBlobIndex struct {
	ParentSnapshotID string
	BlobDigest       string
	SnapshotID       string
}

type MirrorImportedLayerDiffIndex struct {
	ParentSnapshotID string
	DiffID           string
	SnapshotID       string
}

const clearMirrorImportedLayerDiffIndex = `DELETE FROM imported_layer_diff_index`
const clearMirrorImportedLayerBlobIndex = `DELETE FROM imported_layer_blob_index`
const clearMirrorSnapshotContentLinks = `DELETE FROM snapshot_content_links`
const clearMirrorResultSnapshotLinks = `DELETE FROM result_snapshot_links`
const clearMirrorPersistedEdges = `DELETE FROM persisted_edges`
const clearMirrorResultDeps = `DELETE FROM result_deps`
const clearMirrorResultOutputEqClasses = `DELETE FROM result_output_eq_classes`
const clearMirrorTermInputs = `DELETE FROM term_inputs`
const clearMirrorTerms = `DELETE FROM terms`
const clearMirrorEqClassDigests = `DELETE FROM eq_class_digests`
const clearMirrorResults = `DELETE FROM results`
const clearMirrorEqClasses = `DELETE FROM eq_classes`

func (q *Queries) ClearMirrorState(ctx context.Context) error {
	for _, stmt := range []string{
		clearMirrorImportedLayerDiffIndex,
		clearMirrorImportedLayerBlobIndex,
		clearMirrorSnapshotContentLinks,
		clearMirrorResultSnapshotLinks,
		clearMirrorPersistedEdges,
		clearMirrorResultDeps,
		clearMirrorResultOutputEqClasses,
		clearMirrorTermInputs,
		clearMirrorTerms,
		clearMirrorEqClassDigests,
		clearMirrorResults,
		clearMirrorEqClasses,
	} {
		if _, err := q.exec(ctx, nil, stmt); err != nil {
			return err
		}
	}
	return nil
}

const insertMirrorResult = `
INSERT INTO results (
	id, call_frame_json, self_payload, output_effect_ids_json,
	expires_at_unix, created_at_unix_nano,
	last_used_at_unix_nano, record_type, description
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`

func (q *Queries) InsertMirrorResult(ctx context.Context, arg MirrorResult) error {
	_, err := q.exec(ctx, nil, insertMirrorResult,
		arg.ID, arg.CallFrameJSON, arg.SelfPayload, arg.OutputEffectIDs,
		arg.ExpiresAtUnix, arg.CreatedAtUnixNano, arg.LastUsedAtUnixNano,
		arg.RecordType, arg.Description,
	)
	return err
}

const insertMirrorEqClass = `INSERT INTO eq_classes (id) VALUES (?)`

func (q *Queries) InsertMirrorEqClass(ctx context.Context, arg MirrorEqClass) error {
	_, err := q.exec(ctx, nil, insertMirrorEqClass, arg.ID)
	return err
}

const insertMirrorEqClassDigest = `
INSERT INTO eq_class_digests (eq_class_id, digest, label) VALUES (?, ?, ?)
`

func (q *Queries) InsertMirrorEqClassDigest(ctx context.Context, arg MirrorEqClassDigest) error {
	_, err := q.exec(ctx, nil, insertMirrorEqClassDigest, arg.EqClassID, arg.Digest, arg.Label)
	return err
}

const insertMirrorTerm = `
INSERT INTO terms (id, self_digest, term_digest, output_eq_class_id) VALUES (?, ?, ?, ?)
`

func (q *Queries) InsertMirrorTerm(ctx context.Context, arg MirrorTerm) error {
	_, err := q.exec(ctx, nil, insertMirrorTerm, arg.ID, arg.SelfDigest, arg.TermDigest, arg.OutputEqClassID)
	return err
}

const insertMirrorTermInput = `
INSERT INTO term_inputs (term_id, position, input_eq_class_id, provenance_kind) VALUES (?, ?, ?, ?)
`

func (q *Queries) InsertMirrorTermInput(ctx context.Context, arg MirrorTermInput) error {
	_, err := q.exec(ctx, nil, insertMirrorTermInput, arg.TermID, arg.Position, arg.InputEqClassID, arg.ProvenanceKind)
	return err
}

const insertMirrorResultOutputEqClass = `
INSERT INTO result_output_eq_classes (result_id, eq_class_id) VALUES (?, ?)
`

func (q *Queries) InsertMirrorResultOutputEqClass(ctx context.Context, arg MirrorResultOutputEqClass) error {
	_, err := q.exec(ctx, nil, insertMirrorResultOutputEqClass, arg.ResultID, arg.EqClassID)
	return err
}

const insertMirrorResultDep = `
INSERT INTO result_deps (parent_result_id, dep_result_id) VALUES (?, ?)
`

func (q *Queries) InsertMirrorResultDep(ctx context.Context, arg MirrorResultDep) error {
	_, err := q.exec(ctx, nil, insertMirrorResultDep, arg.ParentResultID, arg.DepResultID)
	return err
}

const insertMirrorPersistedEdge = `
INSERT INTO persisted_edges (result_id, created_at_unix_nano, expires_at_unix, unpruneable) VALUES (?, ?, ?, ?)
`

func (q *Queries) InsertMirrorPersistedEdge(ctx context.Context, arg MirrorPersistedEdge) error {
	_, err := q.exec(ctx, nil, insertMirrorPersistedEdge, arg.ResultID, arg.CreatedAtUnixNano, arg.ExpiresAtUnix, arg.Unpruneable)
	return err
}

const insertMirrorResultSnapshotLink = `
INSERT INTO result_snapshot_links (result_id, ref_key, role) VALUES (?, ?, ?)
`

func (q *Queries) InsertMirrorResultSnapshotLink(ctx context.Context, arg MirrorResultSnapshotLink) error {
	_, err := q.exec(ctx, nil, insertMirrorResultSnapshotLink, arg.ResultID, arg.RefKey, arg.Role)
	return err
}

const insertMirrorSnapshotContentLink = `
INSERT INTO snapshot_content_links (snapshot_id, digest) VALUES (?, ?)
`

func (q *Queries) InsertMirrorSnapshotContentLink(ctx context.Context, arg MirrorSnapshotContentLink) error {
	_, err := q.exec(ctx, nil, insertMirrorSnapshotContentLink, arg.SnapshotID, arg.Digest)
	return err
}

const insertMirrorImportedLayerBlobIndex = `
INSERT INTO imported_layer_blob_index (parent_snapshot_id, blob_digest, snapshot_id) VALUES (?, ?, ?)
`

func (q *Queries) InsertMirrorImportedLayerBlobIndex(ctx context.Context, arg MirrorImportedLayerBlobIndex) error {
	_, err := q.exec(ctx, nil, insertMirrorImportedLayerBlobIndex, arg.ParentSnapshotID, arg.BlobDigest, arg.SnapshotID)
	return err
}

const insertMirrorImportedLayerDiffIndex = `
INSERT INTO imported_layer_diff_index (parent_snapshot_id, diff_id, snapshot_id) VALUES (?, ?, ?)
`

func (q *Queries) InsertMirrorImportedLayerDiffIndex(ctx context.Context, arg MirrorImportedLayerDiffIndex) error {
	_, err := q.exec(ctx, nil, insertMirrorImportedLayerDiffIndex, arg.ParentSnapshotID, arg.DiffID, arg.SnapshotID)
	return err
}

const listMirrorResults = `
SELECT
	id, call_frame_json, self_payload, output_effect_ids_json,
	expires_at_unix, created_at_unix_nano,
	last_used_at_unix_nano, record_type, description
FROM results
`

func (q *Queries) ListMirrorResults(ctx context.Context) ([]MirrorResult, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorResults)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MirrorResult
	for rows.Next() {
		var row MirrorResult
		if err := rows.Scan(
			&row.ID,
			&row.CallFrameJSON,
			&row.SelfPayload,
			&row.OutputEffectIDs,
			&row.ExpiresAtUnix,
			&row.CreatedAtUnixNano,
			&row.LastUsedAtUnixNano,
			&row.RecordType,
			&row.Description,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorEqClasses = `SELECT id FROM eq_classes`

func (q *Queries) ListMirrorEqClasses(ctx context.Context) ([]MirrorEqClass, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorEqClasses)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorEqClass
	for rows.Next() {
		var row MirrorEqClass
		if err := rows.Scan(&row.ID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorPersistedEdges = `
SELECT result_id, created_at_unix_nano, expires_at_unix, unpruneable FROM persisted_edges
`

func (q *Queries) ListMirrorPersistedEdges(ctx context.Context) ([]MirrorPersistedEdge, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorPersistedEdges)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorPersistedEdge
	for rows.Next() {
		var row MirrorPersistedEdge
		if err := rows.Scan(&row.ResultID, &row.CreatedAtUnixNano, &row.ExpiresAtUnix, &row.Unpruneable); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorEqClassDigests = `SELECT eq_class_id, digest, label FROM eq_class_digests`

func (q *Queries) ListMirrorEqClassDigests(ctx context.Context) ([]MirrorEqClassDigest, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorEqClassDigests)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorEqClassDigest
	for rows.Next() {
		var row MirrorEqClassDigest
		if err := rows.Scan(&row.EqClassID, &row.Digest, &row.Label); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorTerms = `SELECT id, self_digest, term_digest, output_eq_class_id FROM terms`

func (q *Queries) ListMirrorTerms(ctx context.Context) ([]MirrorTerm, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorTerms)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorTerm
	for rows.Next() {
		var row MirrorTerm
		if err := rows.Scan(&row.ID, &row.SelfDigest, &row.TermDigest, &row.OutputEqClassID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorTermInputs = `SELECT term_id, position, input_eq_class_id, provenance_kind FROM term_inputs`

func (q *Queries) ListMirrorTermInputs(ctx context.Context) ([]MirrorTermInput, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorTermInputs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorTermInput
	for rows.Next() {
		var row MirrorTermInput
		if err := rows.Scan(&row.TermID, &row.Position, &row.InputEqClassID, &row.ProvenanceKind); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorResultOutputEqClasses = `SELECT result_id, eq_class_id FROM result_output_eq_classes`

func (q *Queries) ListMirrorResultOutputEqClasses(ctx context.Context) ([]MirrorResultOutputEqClass, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorResultOutputEqClasses)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorResultOutputEqClass
	for rows.Next() {
		var row MirrorResultOutputEqClass
		if err := rows.Scan(&row.ResultID, &row.EqClassID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorResultDeps = `SELECT parent_result_id, dep_result_id FROM result_deps`

func (q *Queries) ListMirrorResultDeps(ctx context.Context) ([]MirrorResultDep, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorResultDeps)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorResultDep
	for rows.Next() {
		var row MirrorResultDep
		if err := rows.Scan(&row.ParentResultID, &row.DepResultID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorResultSnapshotLinks = `SELECT result_id, ref_key, role FROM result_snapshot_links`

func (q *Queries) ListMirrorResultSnapshotLinks(ctx context.Context) ([]MirrorResultSnapshotLink, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorResultSnapshotLinks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorResultSnapshotLink
	for rows.Next() {
		var row MirrorResultSnapshotLink
		if err := rows.Scan(&row.ResultID, &row.RefKey, &row.Role); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorSnapshotContentLinks = `SELECT snapshot_id, digest FROM snapshot_content_links`

func (q *Queries) ListMirrorSnapshotContentLinks(ctx context.Context) ([]MirrorSnapshotContentLink, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorSnapshotContentLinks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorSnapshotContentLink
	for rows.Next() {
		var row MirrorSnapshotContentLink
		if err := rows.Scan(&row.SnapshotID, &row.Digest); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorImportedLayerBlobIndex = `SELECT parent_snapshot_id, blob_digest, snapshot_id FROM imported_layer_blob_index`

func (q *Queries) ListMirrorImportedLayerBlobIndex(ctx context.Context) ([]MirrorImportedLayerBlobIndex, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorImportedLayerBlobIndex)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorImportedLayerBlobIndex
	for rows.Next() {
		var row MirrorImportedLayerBlobIndex
		if err := rows.Scan(&row.ParentSnapshotID, &row.BlobDigest, &row.SnapshotID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorImportedLayerDiffIndex = `SELECT parent_snapshot_id, diff_id, snapshot_id FROM imported_layer_diff_index`

func (q *Queries) ListMirrorImportedLayerDiffIndex(ctx context.Context) ([]MirrorImportedLayerDiffIndex, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorImportedLayerDiffIndex)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorImportedLayerDiffIndex
	for rows.Next() {
		var row MirrorImportedLayerDiffIndex
		if err := rows.Scan(&row.ParentSnapshotID, &row.DiffID, &row.SnapshotID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
