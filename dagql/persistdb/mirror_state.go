package persistdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// MirrorResult is an entry's results row, with its record row
// (result_records), CallFrameJSON and SelfPayload, beside it: a save writes
// the two apart (UpsertMirrorResult, UpsertMirrorResultRecord), and a
// restore reads them together (EachMirrorResult).
type MirrorResult struct {
	ID              int64
	CallFrameJSON   string
	SelfPayload     []byte
	OutputEffectIDs string
	ExpiresAtUnix   int64
	Replacements    int64
	Indexed         bool
	// CloudHoldingNumber, CloudHoldingStored and CloudHoldingExpiresAtUnix
	// are the entry's Cloud holding: the counterpart the latest message
	// named, whether it stores a record, and that record's expiry. A number
	// of 0 means none.
	CloudHoldingNumber        int64
	CloudHoldingStored        bool
	CloudHoldingExpiresAtUnix int64
	CreatedAtUnixNano         int64
	LastUsedAtUnixNano        int64
	RecordType                string
	Description               string
	// ChangeStamp is, on a blob-backed cache, the entry's change stamp
	// (dagql's sharedResult.changedAt), 0 on any other.
	ChangeStamp int64
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

type MirrorResultCall struct {
	ResultID      int64
	RequestDigest string
	Call          string
}

type MirrorResultTaught struct {
	ResultID int64
	Position int64
	Digest   string
	Given    bool
}

type MirrorPersistedEdge struct {
	ResultID          int64
	CreatedAtUnixNano int64
	ExpiresAtUnix     int64
	Unpruneable       bool
}

type MirrorResultSnapshotLink struct {
	OutputPath string
	ResultID   int64
	RefKey     string
	Role       string
}

type MirrorSnapshotContentLink struct {
	SnapshotID string
	Digest     string
}

type MirrorSnapshotContentHash struct {
	SnapshotID string
	Data       []byte
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

// mirrorWrite names one of the statements a save writes the mirror with, so
// it can run each from a statement prepared once (PrepareMirrorWrites).
type mirrorWrite uint8

const (
	mirrorUpsertResult mirrorWrite = iota
	mirrorUpsertResultRecord
	mirrorInsertEqClass
	mirrorInsertEqClassDigest
	mirrorUpsertTerm
	mirrorInsertTermInput
	mirrorInsertResultOutputEqClass
	mirrorInsertResultDep
	mirrorInsertResultCall
	mirrorInsertResultTaught
	mirrorInsertPersistedEdge
	mirrorInsertResultSnapshotLink
	mirrorInsertSnapshotContentLink
	mirrorInsertSnapshotContentHash
	mirrorInsertImportedLayerBlobIndex
	mirrorInsertImportedLayerDiffIndex
	mirrorDeleteResult
	mirrorDeleteResultRecord
	mirrorDeleteEqClass
	mirrorDeleteEqClassDigests
	mirrorDeleteTerm
	mirrorDeleteTermInputs
	mirrorDeleteResultOutputEqClasses
	mirrorDeleteResultDeps
	mirrorDeleteResultCalls
	mirrorDeleteResultTaught
	mirrorDeletePersistedEdge
	mirrorDeleteResultSnapshotLinks
	mirrorWrites
)

var mirrorWriteSQL = [mirrorWrites]string{
	mirrorUpsertResult:                 upsertMirrorResult,
	mirrorUpsertResultRecord:           upsertMirrorResultRecord,
	mirrorInsertEqClass:                insertMirrorEqClass,
	mirrorInsertEqClassDigest:          insertMirrorEqClassDigest,
	mirrorUpsertTerm:                   upsertMirrorTerm,
	mirrorInsertTermInput:              insertMirrorTermInput,
	mirrorInsertResultOutputEqClass:    insertMirrorResultOutputEqClass,
	mirrorInsertResultDep:              insertMirrorResultDep,
	mirrorInsertResultCall:             insertMirrorResultCall,
	mirrorInsertResultTaught:           insertMirrorResultTaught,
	mirrorInsertPersistedEdge:          insertMirrorPersistedEdge,
	mirrorInsertResultSnapshotLink:     insertMirrorResultSnapshotLink,
	mirrorInsertSnapshotContentLink:    insertMirrorSnapshotContentLink,
	mirrorInsertSnapshotContentHash:    insertMirrorSnapshotContentHash,
	mirrorInsertImportedLayerBlobIndex: insertMirrorImportedLayerBlobIndex,
	mirrorInsertImportedLayerDiffIndex: insertMirrorImportedLayerDiffIndex,
	mirrorDeleteResult:                 `DELETE FROM results WHERE id = ?`,
	mirrorDeleteResultRecord:           `DELETE FROM result_records WHERE result_id = ?`,
	mirrorDeleteEqClass:                `DELETE FROM eq_classes WHERE id = ?`,
	mirrorDeleteEqClassDigests:         `DELETE FROM eq_class_digests WHERE eq_class_id = ?`,
	mirrorDeleteTerm:                   `DELETE FROM terms WHERE id = ?`,
	mirrorDeleteTermInputs:             `DELETE FROM term_inputs WHERE term_id = ?`,
	mirrorDeleteResultOutputEqClasses:  `DELETE FROM result_output_eq_classes WHERE result_id = ?`,
	mirrorDeleteResultDeps:             `DELETE FROM result_deps WHERE parent_result_id = ?`,
	mirrorDeleteResultCalls:            `DELETE FROM result_calls WHERE result_id = ?`,
	mirrorDeleteResultTaught:           `DELETE FROM result_taught WHERE result_id = ?`,
	mirrorDeletePersistedEdge:          `DELETE FROM persisted_edges WHERE result_id = ?`,
	mirrorDeleteResultSnapshotLinks:    `DELETE FROM result_snapshot_links WHERE result_id = ?`,
}

// PrepareMirrorWrites prepares the statements a save writes the mirror with
// on q's transaction, so each is compiled once however many rows it writes.
// The transaction closes them when it ends.
func (q *Queries) PrepareMirrorWrites(ctx context.Context) error {
	if q.tx == nil {
		return errors.New("prepare mirror writes: not in a transaction")
	}
	var stmts [mirrorWrites]*sql.Stmt
	for i, query := range mirrorWriteSQL {
		stmt, err := q.tx.PrepareContext(ctx, query)
		if err != nil {
			return fmt.Errorf("prepare mirror write %d: %w", i, err)
		}
		stmts[i] = stmt
	}
	q.writes = &stmts
	return nil
}

// execWrite runs one of the mirror's writes with args: from its prepared
// statement when the transaction has them, from its SQL otherwise.
func (q *Queries) execWrite(ctx context.Context, which mirrorWrite, args ...any) error {
	var err error
	if q.writes != nil {
		_, err = q.writes[which].ExecContext(ctx, args...)
	} else {
		_, err = q.db.ExecContext(ctx, mirrorWriteSQL[which], args...)
	}
	return err
}

// ClearMirrorSnapshotMetadata deletes the rows of an engine's snapshot
// metadata, which a save writes whole (snapshot_content_links,
// snapshot_content_hashes and the imported layer indexes). A blob-backed
// cache has none.
func (q *Queries) ClearMirrorSnapshotMetadata(ctx context.Context) error {
	for _, stmt := range []string{
		`DELETE FROM imported_layer_diff_index`,
		`DELETE FROM imported_layer_blob_index`,
		`DELETE FROM snapshot_content_links`,
		`DELETE FROM snapshot_content_hashes`,
	} {
		if _, err := q.exec(ctx, nil, stmt); err != nil {
			return err
		}
	}
	return nil
}

// The deletes of one unit's rows: an entry's, by its number, a term's and a
// class's, by theirs. A save deletes the rows of a kind it writes again, and
// every row of a unit the store no longer holds.

func (q *Queries) DeleteMirrorResult(ctx context.Context, id int64) error {
	return q.execWrite(ctx, mirrorDeleteResult, id)
}

func (q *Queries) DeleteMirrorResultRecord(ctx context.Context, resultID int64) error {
	return q.execWrite(ctx, mirrorDeleteResultRecord, resultID)
}

func (q *Queries) DeleteMirrorEqClass(ctx context.Context, id int64) error {
	return q.execWrite(ctx, mirrorDeleteEqClass, id)
}

func (q *Queries) DeleteMirrorEqClassDigests(ctx context.Context, eqClassID int64) error {
	return q.execWrite(ctx, mirrorDeleteEqClassDigests, eqClassID)
}

func (q *Queries) DeleteMirrorTerm(ctx context.Context, id int64) error {
	return q.execWrite(ctx, mirrorDeleteTerm, id)
}

func (q *Queries) DeleteMirrorTermInputs(ctx context.Context, termID int64) error {
	return q.execWrite(ctx, mirrorDeleteTermInputs, termID)
}

func (q *Queries) DeleteMirrorResultOutputEqClasses(ctx context.Context, resultID int64) error {
	return q.execWrite(ctx, mirrorDeleteResultOutputEqClasses, resultID)
}

func (q *Queries) DeleteMirrorResultDeps(ctx context.Context, parentResultID int64) error {
	return q.execWrite(ctx, mirrorDeleteResultDeps, parentResultID)
}

func (q *Queries) DeleteMirrorResultCalls(ctx context.Context, resultID int64) error {
	return q.execWrite(ctx, mirrorDeleteResultCalls, resultID)
}

func (q *Queries) DeleteMirrorResultTaught(ctx context.Context, resultID int64) error {
	return q.execWrite(ctx, mirrorDeleteResultTaught, resultID)
}

func (q *Queries) DeleteMirrorPersistedEdge(ctx context.Context, resultID int64) error {
	return q.execWrite(ctx, mirrorDeletePersistedEdge, resultID)
}

func (q *Queries) DeleteMirrorResultSnapshotLinks(ctx context.Context, resultID int64) error {
	return q.execWrite(ctx, mirrorDeleteResultSnapshotLinks, resultID)
}

const upsertMirrorResult = `
INSERT INTO results (
	id, output_effect_ids_json,
	expires_at_unix, replacements, indexed, cloud_holding_number,
	cloud_holding_stored, cloud_holding_expires_at_unix,
	created_at_unix_nano, last_used_at_unix_nano, record_type, description,
	change_stamp
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
	output_effect_ids_json = excluded.output_effect_ids_json,
	expires_at_unix = excluded.expires_at_unix,
	replacements = excluded.replacements,
	indexed = excluded.indexed,
	cloud_holding_number = excluded.cloud_holding_number,
	cloud_holding_stored = excluded.cloud_holding_stored,
	cloud_holding_expires_at_unix = excluded.cloud_holding_expires_at_unix,
	created_at_unix_nano = excluded.created_at_unix_nano,
	last_used_at_unix_nano = excluded.last_used_at_unix_nano,
	record_type = excluded.record_type,
	description = excluded.description,
	change_stamp = excluded.change_stamp
`

// UpsertMirrorResult writes an entry's results row, all but its record
// (UpsertMirrorResultRecord). An entry's row is updated in place, never
// deleted and inserted again, so a write of one entry reaches no row that
// names it.
func (q *Queries) UpsertMirrorResult(ctx context.Context, arg MirrorResult) error {
	return q.execWrite(ctx, mirrorUpsertResult,
		arg.ID, arg.OutputEffectIDs,
		arg.ExpiresAtUnix, arg.Replacements, arg.Indexed, arg.CloudHoldingNumber,
		arg.CloudHoldingStored, arg.CloudHoldingExpiresAtUnix,
		arg.CreatedAtUnixNano, arg.LastUsedAtUnixNano, arg.RecordType,
		arg.Description, arg.ChangeStamp,
	)
}

const upsertMirrorResultRecord = `
INSERT INTO result_records (result_id, call_frame_json, self_payload) VALUES (?, ?, ?)
ON CONFLICT (result_id) DO UPDATE SET
	call_frame_json = excluded.call_frame_json,
	self_payload = excluded.self_payload
`

// UpsertMirrorResultRecord writes an entry's record row: its call frame and
// its self payload.
func (q *Queries) UpsertMirrorResultRecord(ctx context.Context, arg MirrorResult) error {
	return q.execWrite(ctx, mirrorUpsertResultRecord, arg.ID, arg.CallFrameJSON, arg.SelfPayload)
}

const insertMirrorEqClass = `INSERT INTO eq_classes (id) VALUES (?) ON CONFLICT (id) DO NOTHING`

func (q *Queries) InsertMirrorEqClass(ctx context.Context, arg MirrorEqClass) error {
	return q.execWrite(ctx, mirrorInsertEqClass, arg.ID)
}

const insertMirrorEqClassDigest = `
INSERT INTO eq_class_digests (eq_class_id, digest, label) VALUES (?, ?, ?)
`

func (q *Queries) InsertMirrorEqClassDigest(ctx context.Context, arg MirrorEqClassDigest) error {
	return q.execWrite(ctx, mirrorInsertEqClassDigest, arg.EqClassID, arg.Digest, arg.Label)
}

const upsertMirrorTerm = `
INSERT INTO terms (id, self_digest, term_digest, output_eq_class_id) VALUES (?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
	self_digest = excluded.self_digest,
	term_digest = excluded.term_digest,
	output_eq_class_id = excluded.output_eq_class_id
`

func (q *Queries) UpsertMirrorTerm(ctx context.Context, arg MirrorTerm) error {
	return q.execWrite(ctx, mirrorUpsertTerm, arg.ID, arg.SelfDigest, arg.TermDigest, arg.OutputEqClassID)
}

const insertMirrorTermInput = `
INSERT INTO term_inputs (term_id, position, input_eq_class_id, provenance_kind) VALUES (?, ?, ?, ?)
`

func (q *Queries) InsertMirrorTermInput(ctx context.Context, arg MirrorTermInput) error {
	return q.execWrite(ctx, mirrorInsertTermInput, arg.TermID, arg.Position, arg.InputEqClassID, arg.ProvenanceKind)
}

const insertMirrorResultOutputEqClass = `
INSERT INTO result_output_eq_classes (result_id, eq_class_id) VALUES (?, ?)
`

func (q *Queries) InsertMirrorResultOutputEqClass(ctx context.Context, arg MirrorResultOutputEqClass) error {
	return q.execWrite(ctx, mirrorInsertResultOutputEqClass, arg.ResultID, arg.EqClassID)
}

const insertMirrorResultDep = `
INSERT INTO result_deps (parent_result_id, dep_result_id) VALUES (?, ?)
`

func (q *Queries) InsertMirrorResultDep(ctx context.Context, arg MirrorResultDep) error {
	return q.execWrite(ctx, mirrorInsertResultDep, arg.ParentResultID, arg.DepResultID)
}

const insertMirrorResultCall = `
INSERT INTO result_calls (result_id, request_digest, call) VALUES (?, ?, ?)
`

func (q *Queries) InsertMirrorResultCall(ctx context.Context, arg MirrorResultCall) error {
	return q.execWrite(ctx, mirrorInsertResultCall, arg.ResultID, arg.RequestDigest, arg.Call)
}

const insertMirrorResultTaught = `
INSERT INTO result_taught (result_id, position, digest, given) VALUES (?, ?, ?, ?)
`

func (q *Queries) InsertMirrorResultTaught(ctx context.Context, arg MirrorResultTaught) error {
	return q.execWrite(ctx, mirrorInsertResultTaught, arg.ResultID, arg.Position, arg.Digest, arg.Given)
}

const insertMirrorPersistedEdge = `
INSERT INTO persisted_edges (result_id, created_at_unix_nano, expires_at_unix, unpruneable) VALUES (?, ?, ?, ?)
`

func (q *Queries) InsertMirrorPersistedEdge(ctx context.Context, arg MirrorPersistedEdge) error {
	return q.execWrite(ctx, mirrorInsertPersistedEdge, arg.ResultID, arg.CreatedAtUnixNano, arg.ExpiresAtUnix, arg.Unpruneable)
}

const insertMirrorResultSnapshotLink = `
INSERT INTO result_snapshot_links (result_id, ref_key, role, output_path) VALUES (?, ?, ?, ?)
`

func (q *Queries) InsertMirrorResultSnapshotLink(ctx context.Context, arg MirrorResultSnapshotLink) error {
	return q.execWrite(ctx, mirrorInsertResultSnapshotLink, arg.ResultID, arg.RefKey, arg.Role, arg.OutputPath)
}

const insertMirrorSnapshotContentLink = `
INSERT INTO snapshot_content_links (snapshot_id, digest) VALUES (?, ?)
`

func (q *Queries) InsertMirrorSnapshotContentLink(ctx context.Context, arg MirrorSnapshotContentLink) error {
	return q.execWrite(ctx, mirrorInsertSnapshotContentLink, arg.SnapshotID, arg.Digest)
}

const insertMirrorSnapshotContentHash = `
INSERT INTO snapshot_content_hashes (snapshot_id, data) VALUES (?, ?)
`

func (q *Queries) InsertMirrorSnapshotContentHash(ctx context.Context, arg MirrorSnapshotContentHash) error {
	return q.execWrite(ctx, mirrorInsertSnapshotContentHash, arg.SnapshotID, arg.Data)
}

const insertMirrorImportedLayerBlobIndex = `
INSERT INTO imported_layer_blob_index (parent_snapshot_id, blob_digest, snapshot_id) VALUES (?, ?, ?)
`

func (q *Queries) InsertMirrorImportedLayerBlobIndex(ctx context.Context, arg MirrorImportedLayerBlobIndex) error {
	return q.execWrite(ctx, mirrorInsertImportedLayerBlobIndex, arg.ParentSnapshotID, arg.BlobDigest, arg.SnapshotID)
}

const insertMirrorImportedLayerDiffIndex = `
INSERT INTO imported_layer_diff_index (parent_snapshot_id, diff_id, snapshot_id) VALUES (?, ?, ?)
`

func (q *Queries) InsertMirrorImportedLayerDiffIndex(ctx context.Context, arg MirrorImportedLayerDiffIndex) error {
	return q.execWrite(ctx, mirrorInsertImportedLayerDiffIndex, arg.ParentSnapshotID, arg.DiffID, arg.SnapshotID)
}

// listMirrorResults reads each entry's results row with its record row. A
// row without a record reads with an empty one, which the import refuses by
// name.
const listMirrorResults = `
SELECT
	r.id, COALESCE(rr.call_frame_json, ''), COALESCE(rr.self_payload, X''), r.output_effect_ids_json,
	r.expires_at_unix, r.replacements, r.indexed, r.cloud_holding_number,
	r.cloud_holding_stored, r.cloud_holding_expires_at_unix,
	r.created_at_unix_nano, r.last_used_at_unix_nano, r.record_type, r.description,
	r.change_stamp
FROM results r LEFT JOIN result_records rr ON rr.result_id = r.id
ORDER BY r.id
`

func (q *Queries) ListMirrorResults(ctx context.Context) ([]MirrorResult, error) {
	var out []MirrorResult
	err := q.EachMirrorResult(ctx, func(row MirrorResult) error {
		out = append(out, row)
		return nil
	})
	return out, err
}

// EachMirrorResult calls fn with each results row as it is read, so the
// caller decides what of a row outlives the call. An error from fn stops the
// read and is returned.
func (q *Queries) EachMirrorResult(ctx context.Context, fn func(MirrorResult) error) error {
	rows, err := q.db.QueryContext(ctx, listMirrorResults)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var row MirrorResult
		if err := rows.Scan(
			&row.ID,
			&row.CallFrameJSON,
			&row.SelfPayload,
			&row.OutputEffectIDs,
			&row.ExpiresAtUnix,
			&row.Replacements,
			&row.Indexed,
			&row.CloudHoldingNumber,
			&row.CloudHoldingStored,
			&row.CloudHoldingExpiresAtUnix,
			&row.CreatedAtUnixNano,
			&row.LastUsedAtUnixNano,
			&row.RecordType,
			&row.Description,
			&row.ChangeStamp,
		); err != nil {
			return err
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	return rows.Err()
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

const listMirrorResultCalls = `
SELECT result_id, request_digest, call FROM result_calls
`

func (q *Queries) ListMirrorResultCalls(ctx context.Context) ([]MirrorResultCall, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorResultCalls)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorResultCall
	for rows.Next() {
		var row MirrorResultCall
		if err := rows.Scan(&row.ResultID, &row.RequestDigest, &row.Call); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorResultTaught = `
SELECT result_id, position, digest, given FROM result_taught ORDER BY result_id, position
`

func (q *Queries) ListMirrorResultTaught(ctx context.Context) ([]MirrorResultTaught, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorResultTaught)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorResultTaught
	for rows.Next() {
		var row MirrorResultTaught
		if err := rows.Scan(&row.ResultID, &row.Position, &row.Digest, &row.Given); err != nil {
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

const listMirrorResultSnapshotLinks = `SELECT result_id, ref_key, role, output_path FROM result_snapshot_links`

func (q *Queries) ListMirrorResultSnapshotLinks(ctx context.Context) ([]MirrorResultSnapshotLink, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorResultSnapshotLinks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorResultSnapshotLink
	for rows.Next() {
		var row MirrorResultSnapshotLink
		if err := rows.Scan(&row.ResultID, &row.RefKey, &row.Role, &row.OutputPath); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const listMirrorSnapshotContentHashes = `SELECT snapshot_id, data FROM snapshot_content_hashes`

func (q *Queries) ListMirrorSnapshotContentHashes(ctx context.Context) ([]MirrorSnapshotContentHash, error) {
	rows, err := q.db.QueryContext(ctx, listMirrorSnapshotContentHashes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorSnapshotContentHash
	for rows.Next() {
		var row MirrorSnapshotContentHash
		if err := rows.Scan(&row.SnapshotID, &row.Data); err != nil {
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
