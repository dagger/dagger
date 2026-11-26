package dagql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/maphash"
	"slices"
	"strconv"

	persistdb "github.com/dagger/dagger/dagql/persistdb"
	"github.com/dagger/dagger/engine/slog"
)

// Checkpoint saves a blob-backed cache, the Cloud's, without closing it: the
// same save as Close's, the closure of the stored roots in one transaction,
// with each value's stored parts. A crash of the process during a save keeps
// the previous one, and a blob-backed cache restores the last save however
// its process ended (NewCache). Its store is written with synchronous=NORMAL
// (prepareCacheDBs), so an operating-system crash or a power loss leaves it
// whole too, though it can lose the latest save. Only a blob-backed cache
// checkpoints while it runs: it never decodes a value, so its save copies
// stored records as they are.
//
// SQLite writes a save to the write-ahead log first, so the log grows to
// about the store's size, and SQLite then reuses the file without shrinking
// it. So after the save, Checkpoint copies the log into the database, which
// syncs it, and truncates the log: between saves, the store takes its own
// size on disk, not twice that. A checkpoint that another connection keeps
// from completing, with a lock it holds, leaves the log as it is: the save
// stands, and a later one truncates it.
func (c *Cache) Checkpoint(ctx context.Context) error {
	if !c.blobBacked {
		return errors.New("checkpoint: the cache has no blob store")
	}
	op, err := c.beginCacheOperation()
	if err != nil {
		return err
	}
	defer op.finish(false)
	if c.sqlDB == nil || c.pdb == nil {
		return errors.New("checkpoint: the cache has no persistence database")
	}
	c.checkpointMu.Lock()
	defer c.checkpointMu.Unlock()
	if err := c.persistCurrentState(ctx); err != nil {
		return err
	}
	var busy, logFrames, copied int
	if err := c.sqlDB.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &copied); err != nil {
		return fmt.Errorf("checkpoint: saved, but truncating the write-ahead log failed: %w", err)
	}
	if busy != 0 {
		slog.Warn("dagql checkpoint saved, but the write-ahead log is in use and stays at its size", "frames", logFrames, "copied", copied)
	}
	return nil
}

func (c *Cache) persistCurrentState(ctx context.Context) error {
	if c.sqlDB == nil || c.pdb == nil {
		return nil
	}

	c.prepareMergedPayloadsForSave(ctx)
	snapshot, err := c.snapshotPersistState(ctx)
	if err != nil {
		return err
	}
	if err := c.applyPersistStateSnapshot(ctx, c.sqlDB, c.pdb, &snapshot); err != nil {
		return err
	}
	// The database holds what this save wrote: the next save compares with
	// it.
	c.persistKept = snapshot.next
	step := c.lockGraphAs(ctx, "dagql.persist")
	c.persistedResults = len(snapshot.next.entries)
	c.persistedWritten = snapshot.written
	c.persistSnapshotHeld = snapshot.walk.longest
	c.persistWalk = snapshot.walk
	c.unlockGraphAs(step)
	if testPersistFullSave != nil && snapshot.full != nil {
		testPersistFullSave(ctx, c, snapshot.full)
	}
	return nil
}

// testPersistFullSave, when set, is called after each successful save with
// the snapshot of a full save, as an empty database would take it, taken in
// the save's last hold: a test checks that the database the save left holds
// exactly what a full save would have written.
var testPersistFullSave func(ctx context.Context, c *Cache, full *persistStateSnapshot)

// snapshotPersistState reads the store, holding egraphMu for reading in
// pieces (readStore), and works out with no lock held what the save writes:
// what of the store differs from what the database holds (persistKept). A
// Checkpoint runs it beside other readers, so it reads class roots without
// compressing their paths, which writes. It doesn't encode results' rows:
// applyPersistStateSnapshot encodes each as it writes it.
func (c *Cache) snapshotPersistState(ctx context.Context) (persistStateSnapshot, error) {
	if c.persistSeed == (maphash.Seed{}) {
		// A cache that restored nothing keeps nothing yet, so any seed
		// fingerprints it.
		c.persistSeed = maphash.MakeSeed()
	}
	_, step := BeginStep(ctx, "dagql.persist.snapshot")
	rd, walk, nextResultID, writes, fullRead, err := c.readStore()
	step.End(err)
	if err != nil {
		return persistStateSnapshot{}, err
	}
	snapshot, err := rd.assemble(nextResultID, writes)
	if err != nil {
		return persistStateSnapshot{}, err
	}
	snapshot.walk = walk
	if testPersistNotes != nil && rd.prints != nil {
		before := c.testPersistPrints
		if before == nil {
			before = newPersistPrints()
		}
		testPersistNotes(c, before, rd.prints)
		c.testPersistPrints = rd.prints
	}

	if c.snapshotManager != nil {
		rows := c.snapshotManager.PersistentMetadataRows()
		for _, row := range rows.ContentHashes {
			snapshot.snapshotContentHashes = append(snapshot.snapshotContentHashes, persistdb.MirrorSnapshotContentHash{
				SnapshotID: row.SnapshotID,
				Data:       row.Data,
			})
		}
		for _, row := range rows.SnapshotContent {
			snapshot.snapshotContentLinks = append(snapshot.snapshotContentLinks, persistdb.MirrorSnapshotContentLink{
				SnapshotID: row.SnapshotID,
				Digest:     row.Digest.String(),
			})
		}
		for _, row := range rows.ImportedByBlob {
			snapshot.importedLayerByBlob = append(snapshot.importedLayerByBlob, persistdb.MirrorImportedLayerBlobIndex{
				ParentSnapshotID: row.ParentSnapshotID,
				BlobDigest:       row.BlobDigest.String(),
				SnapshotID:       row.SnapshotID,
			})
		}
		for _, row := range rows.ImportedByDiff {
			snapshot.importedLayerByDiff = append(snapshot.importedLayerByDiff, persistdb.MirrorImportedLayerDiffIndex{
				ParentSnapshotID: row.ParentSnapshotID,
				DiffID:           row.DiffID.String(),
				SnapshotID:       row.SnapshotID,
			})
		}
	}
	if fullRead != nil {
		full, err := fullRead.assemble(nextResultID, writes)
		if err != nil {
			return persistStateSnapshot{}, fmt.Errorf("the full save in the last hold: %w", err)
		}
		full.snapshotContentHashes = snapshot.snapshotContentHashes
		full.snapshotContentLinks = snapshot.snapshotContentLinks
		full.importedLayerByBlob = snapshot.importedLayerByBlob
		full.importedLayerByDiff = snapshot.importedLayerByDiff
		snapshot.full = &full
	}
	return snapshot, nil
}

// encodePersistedResultRow encodes one entry's saved row from its snapshot:
// its self payload, its call frame and its snapshot links. It reads only the
// snapshot, so a save calls it for each row it writes, whichever rows those
// are. A blob-backed cache's stored record is saved as it is held
// (encodeStoredRow).
func (c *Cache) encodePersistedResultRow(ctx context.Context, resultSnapshot *persistResultSnapshot) error {
	encoding, err := c.persistResultEnvelope(ctx, resultSnapshot)
	switch {
	case errors.Is(err, ErrPersistStateNotReady):
		return err
	case err != nil:
		return fmt.Errorf("persist result %d envelope: %w", resultSnapshot.resultID, err)
	}
	var payload []byte
	if c.blobBacked && resultSnapshot.persistedEnvelope != nil {
		body := resultSnapshot.stored
		if body == nil {
			// A record the body worker hasn't compressed yet is
			// compressed here, as the worker would.
			if body, err = newStoredBody(&sharedResult{id: resultSnapshot.resultID}, resultSnapshot.persistedEnvelope, resultSnapshot.frame); err != nil {
				return fmt.Errorf("persist result %d stored record: %w", resultSnapshot.resultID, err)
			}
		}
		// A light envelope is shared by the entries of its content, and
		// names no row: the saved record names the entry's own.
		encoding.Envelope.ResultID = uint64(resultSnapshot.resultID)
		payload, err = encodeStoredRow(&encoding.Envelope, body, resultSnapshot.partFacts)
	} else {
		payload, err = json.Marshal(encoding.Envelope)
	}
	if err != nil {
		return fmt.Errorf("persist result %d payload: %w", resultSnapshot.resultID, err)
	}
	switch {
	case resultSnapshot.frame != nil:
		callFrameJSON, err := json.Marshal(resultSnapshot.frame)
		if err != nil {
			return fmt.Errorf("persist result %d call frame JSON: %w", resultSnapshot.resultID, err)
		}
		resultSnapshot.row.CallFrameJSON = string(callFrameJSON)
	case resultSnapshot.stored.holdsFrame():
		// A frame held in the body is written out as it is held: the JSON
		// the frame itself encodes to (storedBody.withFrame).
		callFrameJSON, err := resultSnapshot.stored.frameJSON()
		if err != nil {
			return fmt.Errorf("persist result %d call frame JSON: %w", resultSnapshot.resultID, err)
		}
		resultSnapshot.row.CallFrameJSON = string(callFrameJSON)
	}
	resultSnapshot.row.SelfPayload = payload
	resultSnapshot.resultSnapshotLinks, err = resultSnapshotLinkRows(resultSnapshot.resultID, encoding.SnapshotLinks)
	return err
}

// applyPersistStateSnapshot writes what the snapshot holds to the database
// db and its queries pdb, in one transaction: it deletes the rows of each
// unit the store no longer holds, and writes the rows of each unit that
// changed or is new, the kinds of them whose fingerprints moved. Each record
// it checks it encodes, and writes only if its bytes differ from what the
// database holds. It fills the record fingerprints of the snapshot's next
// kept table as it goes.
//
//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func (c *Cache) applyPersistStateSnapshot(ctx context.Context, db *sql.DB, pdb *persistdb.Queries, snapshot *persistStateSnapshot) (rerr error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin persistence mirror tx: %w", err)
	}
	// The transaction is begun under ctx, so cancelling ctx rolls the save
	// back: database/sql rolls the transaction back once the statement
	// running then ends, and the save's later statements and its commit
	// fail. Its statements run under a context that can't be cancelled: the
	// SQLite driver starts a goroutine to interrupt each statement run under
	// one that can, and a save runs one for each of its rows, millions of
	// them. Each statement is prepared once.
	saveCtx := ctx
	ctx = context.WithoutCancel(ctx)
	defer func() {
		if rerr != nil {
			_ = tx.Rollback()
			if saveCtx.Err() != nil {
				rerr = fmt.Errorf("%w: %w", rerr, context.Cause(saveCtx))
			}
		}
	}()
	q := pdb.WithTx(tx)
	if err := q.PrepareMirrorWrites(ctx); err != nil {
		return fmt.Errorf("prepare persistence mirror writes: %w", err)
	}
	if err := q.UpsertMeta(ctx, persistdb.MetaKeyNextResultID, strconv.FormatUint(uint64(snapshot.nextResultID), 10)); err != nil {
		return fmt.Errorf("save next result ID: %w", err)
	}
	if c.blobBacked {
		if err := q.UpsertMeta(ctx, persistdb.MetaKeyBundleWrites, strconv.FormatUint(snapshot.writes, 10)); err != nil {
			return fmt.Errorf("save write count: %w", err)
		}
	}

	// What the store no longer holds goes, the rows that name a unit
	// before the unit's own.
	for _, id := range snapshot.goneResults {
		for _, del := range []struct {
			table string
			fn    func(context.Context, int64) error
		}{
			{"result_snapshot_links", q.DeleteMirrorResultSnapshotLinks},
			{"persisted_edges", q.DeleteMirrorPersistedEdge},
			{"result_taught", q.DeleteMirrorResultTaught},
			{"result_calls", q.DeleteMirrorResultCalls},
			{"result_deps", q.DeleteMirrorResultDeps},
			{"result_output_eq_classes", q.DeleteMirrorResultOutputEqClasses},
			{"result_records", q.DeleteMirrorResultRecord},
			{"results", q.DeleteMirrorResult},
		} {
			if err := del.fn(ctx, id); err != nil {
				return fmt.Errorf("delete %s of result %d: %w", del.table, id, err)
			}
		}
	}
	for _, id := range snapshot.goneTerms {
		if err := q.DeleteMirrorTermInputs(ctx, id); err != nil {
			return fmt.Errorf("delete term_inputs of term %d: %w", id, err)
		}
		if err := q.DeleteMirrorTerm(ctx, id); err != nil {
			return fmt.Errorf("delete term %d: %w", id, err)
		}
	}
	for _, id := range snapshot.goneClasses {
		if err := q.DeleteMirrorEqClassDigests(ctx, id); err != nil {
			return fmt.Errorf("delete eq_class_digests of eq_class %d: %w", id, err)
		}
		if err := q.DeleteMirrorEqClass(ctx, id); err != nil {
			return fmt.Errorf("delete eq_class %d: %w", id, err)
		}
	}

	for _, class := range snapshot.classes {
		if err := q.InsertMirrorEqClass(ctx, persistdb.MirrorEqClass{ID: class.id}); err != nil {
			return fmt.Errorf("insert eq_class %d: %w", class.id, err)
		}
		if class.held {
			if err := q.DeleteMirrorEqClassDigests(ctx, class.id); err != nil {
				return fmt.Errorf("delete eq_class_digests of eq_class %d: %w", class.id, err)
			}
		}
		for _, row := range class.digests {
			if err := q.InsertMirrorEqClassDigest(ctx, row); err != nil {
				return fmt.Errorf("insert eq_class_digest (%d,%s,%s): %w", row.EqClassID, row.Digest, row.Label, err)
			}
		}
	}
	for i := range snapshot.results {
		result := &snapshot.results[i]
		if result.write&persistWriteRow == 0 {
			continue
		}
		if err := q.UpsertMirrorResult(ctx, result.row); err != nil {
			return fmt.Errorf("upsert result %d: %w", result.resultID, err)
		}
	}
	// Each result's record is encoded just before it is compared and
	// written, and its encoding and the snapshot's copies of its call frame
	// and value are let go once it is: the save holds one record's encoding
	// at a time, not every record's until the commit.
	for i := range snapshot.results {
		result := &snapshot.results[i]
		if result.write&persistWriteRecord == 0 {
			continue
		}
		if result.frame == nil && !result.stored.holdsFrame() {
			if result.self == nil || result.self.Type() == nil || result.self.Type().Name() != "Query" {
				return fmt.Errorf("persist result %d: missing result call frame", result.resultID)
			}
		}
		if err := c.encodePersistedResultRow(ctx, result); err != nil {
			return err
		}
		var links rowsPrint
		for _, row := range result.resultSnapshotLinks {
			links.add(rowPrint(c.persistSeed, row))
		}
		record := recordPrint(c.persistSeed, recordBytesPrint(c.persistSeed, result.row.CallFrameJSON, result.row.SelfPayload), links)
		snapshot.next.entries[result.kept].record = record
		if !result.held || record != result.keptRecord {
			result.recordWritten = true
			if err := q.UpsertMirrorResultRecord(ctx, result.row); err != nil {
				return fmt.Errorf("upsert result_record %d: %w", result.resultID, err)
			}
			if result.held {
				if err := q.DeleteMirrorResultSnapshotLinks(ctx, int64(result.resultID)); err != nil {
					return fmt.Errorf("delete result_snapshot_links of result %d: %w", result.resultID, err)
				}
			}
			for _, row := range result.resultSnapshotLinks {
				if err := q.InsertMirrorResultSnapshotLink(ctx, row); err != nil {
					return fmt.Errorf("insert result_snapshot_link (%d,%s,%s): %w", row.ResultID, row.RefKey, row.Role, err)
				}
			}
		}
		result.row.SelfPayload = nil
		result.row.CallFrameJSON = ""
		result.frame = nil
		result.self = nil
		result.stored = nil
		result.partFacts = nil
	}
	for _, term := range snapshot.terms {
		if err := q.UpsertMirrorTerm(ctx, term.row); err != nil {
			return fmt.Errorf("upsert term %d: %w", term.row.ID, err)
		}
		if term.held {
			if err := q.DeleteMirrorTermInputs(ctx, term.row.ID); err != nil {
				return fmt.Errorf("delete term_inputs of term %d: %w", term.row.ID, err)
			}
		}
		for _, row := range term.inputs {
			if err := q.InsertMirrorTermInput(ctx, row); err != nil {
				return fmt.Errorf("insert term_input (%d,%d): %w", row.TermID, row.Position, err)
			}
		}
	}
	for i := range snapshot.results {
		result := &snapshot.results[i]
		id := int64(result.resultID)
		if result.write&persistWriteClasses != 0 {
			if result.held {
				if err := q.DeleteMirrorResultOutputEqClasses(ctx, id); err != nil {
					return fmt.Errorf("delete result_output_eq_classes of result %d: %w", id, err)
				}
			}
			for _, row := range result.outputEqClasses {
				if err := q.InsertMirrorResultOutputEqClass(ctx, row); err != nil {
					return fmt.Errorf("insert result_output_eq_class (%d,%d): %w", row.ResultID, row.EqClassID, err)
				}
			}
		}
		if result.write&persistWriteCalls != 0 {
			if result.held {
				if err := q.DeleteMirrorResultCalls(ctx, id); err != nil {
					return fmt.Errorf("delete result_calls of result %d: %w", id, err)
				}
			}
			for _, row := range result.calls {
				if err := q.InsertMirrorResultCall(ctx, row); err != nil {
					return fmt.Errorf("insert result_call (%d,%s): %w", row.ResultID, row.RequestDigest, err)
				}
			}
		}
		if result.write&persistWriteTaught != 0 {
			if result.held {
				if err := q.DeleteMirrorResultTaught(ctx, id); err != nil {
					return fmt.Errorf("delete result_taught of result %d: %w", id, err)
				}
			}
			for _, row := range result.taught {
				if err := q.InsertMirrorResultTaught(ctx, row); err != nil {
					return fmt.Errorf("insert result_taught (%d,%d): %w", row.ResultID, row.Position, err)
				}
			}
		}
		if result.write&persistWriteEdge != 0 {
			if result.held {
				if err := q.DeleteMirrorPersistedEdge(ctx, id); err != nil {
					return fmt.Errorf("delete persisted_edge of result %d: %w", id, err)
				}
			}
			if result.edge != nil {
				if err := q.InsertMirrorPersistedEdge(ctx, *result.edge); err != nil {
					return fmt.Errorf("insert persisted_edge (%d): %w", id, err)
				}
			}
		}
	}
	// Dependency edges come after every entry's row, as each names two.
	for i := range snapshot.results {
		result := &snapshot.results[i]
		if result.write&persistWriteDeps == 0 {
			continue
		}
		if result.held {
			if err := q.DeleteMirrorResultDeps(ctx, int64(result.resultID)); err != nil {
				return fmt.Errorf("delete result_deps of result %d: %w", result.resultID, err)
			}
		}
		for _, row := range result.resultDeps {
			if err := q.InsertMirrorResultDep(ctx, row); err != nil {
				return fmt.Errorf("insert result_dep (%d,%d): %w", row.ParentResultID, row.DepResultID, err)
			}
		}
	}
	// An engine's snapshot metadata is written whole: a blob-backed cache
	// has none.
	if err := q.ClearMirrorSnapshotMetadata(ctx); err != nil {
		return fmt.Errorf("clear snapshot metadata: %w", err)
	}
	for _, row := range snapshot.snapshotContentLinks {
		if err := q.InsertMirrorSnapshotContentLink(ctx, row); err != nil {
			return fmt.Errorf("insert snapshot_content_link (%s,%s): %w", row.SnapshotID, row.Digest, err)
		}
	}
	for _, row := range snapshot.snapshotContentHashes {
		if err := q.InsertMirrorSnapshotContentHash(ctx, row); err != nil {
			return fmt.Errorf("insert snapshot_content_hash (%s): %w", row.SnapshotID, err)
		}
	}
	for _, row := range snapshot.importedLayerByBlob {
		if err := q.InsertMirrorImportedLayerBlobIndex(ctx, row); err != nil {
			return fmt.Errorf("insert imported_layer_blob_index (%s,%s,%s): %w", row.ParentSnapshotID, row.BlobDigest, row.SnapshotID, err)
		}
	}
	for _, row := range snapshot.importedLayerByDiff {
		if err := q.InsertMirrorImportedLayerDiffIndex(ctx, row); err != nil {
			return fmt.Errorf("insert imported_layer_diff_index (%s,%s,%s): %w", row.ParentSnapshotID, row.DiffID, row.SnapshotID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit persistence mirror tx: %w", err)
	}
	snapshot.written = 0
	for i := range snapshot.results {
		if result := &snapshot.results[i]; result.recordWritten || result.write&^persistWriteRecord != 0 {
			snapshot.written++
		}
	}
	return nil
}

func resultSnapshotLinkRows(resultID sharedResultID, links []PersistedSnapshotRefLink) ([]persistdb.MirrorResultSnapshotLink, error) {
	rows := make([]persistdb.MirrorResultSnapshotLink, 0, len(links))
	for _, link := range links {
		key, err := snapshotLinkKey(link)
		if err != nil {
			return nil, err
		}
		if link.RefKey == "" {
			return nil, fmt.Errorf("empty snapshot key")
		}
		rows = append(rows, persistdb.MirrorResultSnapshotLink{ResultID: int64(resultID), RefKey: link.RefKey, Role: key.Role, OutputPath: key.Path})
	}
	slices.SortFunc(rows, func(a, b persistdb.MirrorResultSnapshotLink) int {
		if a.OutputPath < b.OutputPath {
			return -1
		}
		if a.OutputPath > b.OutputPath {
			return 1
		}
		if a.Role < b.Role {
			return -1
		}
		if a.Role > b.Role {
			return 1
		}
		return 0
	})
	return rows, nil
}

func (c *Cache) persistResultEnvelope(ctx context.Context, snapshot *persistResultSnapshot) (encoding PersistedResultEncoding, rerr error) {
	defer func() {
		if rerr == nil && snapshot != nil {
			encoding.Envelope.Imported = snapshot.imported
			encoding.Envelope.PersistDeclined = snapshot.persistDeclined
			encoding.Envelope.BoundSession = snapshot.boundSession
			encoding.Envelope.Run, encoding.Envelope.RunRan = snapshot.run, uint8(snapshot.runRan)
			encoding.Envelope.PendingOffers, rerr = clonePartOffers(snapshot.pendingOffers)
			for _, part := range snapshot.storedParts {
				encoding.Envelope.StoredParts = append(encoding.Envelope.StoredParts, part.offer())
			}
		}
	}()
	if snapshot != nil && snapshot.persistedEnvelope != nil {
		return PersistedResultEncoding{
			Envelope:      *snapshot.persistedEnvelope,
			SnapshotLinks: snapshot.snapshotOwnerLinks,
		}, nil
	}
	if snapshot == nil {
		return PersistedResultEncoding{}, fmt.Errorf("persist result envelope: nil snapshot")
	}
	// Shutdown has drained cache operations, but diagnostic readers can still
	// briefly hold object latches. This permission belongs only to the persister,
	// never to live capture through the same codec.
	ctx = context.WithValue(ctx, quiescentPersistKey{}, true)
	if !snapshot.hasValue || snapshot.self == nil {
		// A row without a value is an attached absent value: it keeps its
		// identity and recorded call so restart restores the same row. The
		// declaration is checked here, at capture, so a contradictory row
		// fails this save by name instead of wiping the store on import.
		return encodePersistedAbsentValue(
			NewPersistEncodeContext(c, uint64(snapshot.resultID), snapshot.frame),
			uint64(snapshot.resultID),
			snapshot.sessionResourceHandle,
			true,
		)
	}
	if snapshot.frame == nil {
		if snapshot.self == nil || snapshot.self.Type() == nil || snapshot.self.Type().Name() != "Query" {
			return PersistedResultEncoding{}, fmt.Errorf("result has no call frame and no persisted envelope")
		}
		shared := &sharedResult{
			self:                  snapshot.self,
			isObject:              snapshot.isObject,
			hasValue:              snapshot.hasValue,
			id:                    snapshot.resultID,
			sessionResourceHandle: snapshot.sessionResourceHandle,
		}
		return DefaultPersistedSelfCodec.EncodeResult(context.WithoutCancel(ctx), c, Result[Typed]{shared: shared})
	}
	shared := &sharedResult{
		self:                  snapshot.self,
		isObject:              snapshot.isObject,
		hasValue:              snapshot.hasValue,
		id:                    snapshot.resultID,
		sessionResourceHandle: snapshot.sessionResourceHandle,
	}
	shared.storeResultCall(snapshot.frame)
	persistCtx := context.WithoutCancel(ctx)
	persistCtx = ContextWithCall(persistCtx, snapshot.frame)
	env, err := DefaultPersistedSelfCodec.EncodeResult(persistCtx, c, Result[Typed]{shared: shared})
	if err != nil {
		field := snapshot.frame.Field
		if field == "" {
			field = snapshot.frame.SyntheticOp
		}
		typeName := ""
		if snapshot.frame.Type != nil {
			typeName = snapshot.frame.Type.NamedType
		}
		selfType := ""
		if snapshot.self != nil {
			selfType = snapshot.self.Type().Name()
		}
		slog.Error(
			"persist result envelope encode failed",
			"resultID", snapshot.resultID,
			"recordType", snapshot.row.RecordType,
			"description", snapshot.row.Description,
			"field", field,
			"kind", snapshot.frame.Kind,
			"typeName", typeName,
			"selfType", selfType,
			"hasValue", snapshot.hasValue,
			"sessionResourceHandle", snapshot.sessionResourceHandle,
			"err", err,
		)
		return PersistedResultEncoding{}, err
	}
	return env, nil
}
