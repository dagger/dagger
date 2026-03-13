package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql/call"
	persistdb "github.com/dagger/dagger/dagql/persistdb"
)

func (c *cache) persistCurrentState(ctx context.Context) error {
	if c.sqlDB == nil || c.pdb == nil {
		return nil
	}

	snapshot, err := c.snapshotPersistState(ctx)
	if err != nil {
		return err
	}
	return c.applyPersistStateSnapshot(ctx, snapshot)
}

func (c *cache) snapshotPersistState(ctx context.Context) (persistStateSnapshot, error) {
	var snapshot persistStateSnapshot

	c.egraphMu.RLock()

	addEqClassID := func(eqClassIDs map[eqClassID]struct{}, eqID eqClassID) {
		eqID = c.findEqClassLocked(eqID)
		if eqID == 0 {
			return
		}
		eqClassIDs[eqID] = struct{}{}
	}

	eqClassIDs := make(map[eqClassID]struct{})

	for eqID := range c.eqClassToDigests {
		addEqClassID(eqClassIDs, eqID)
	}
	for eqID := range c.eqClassExtraDigests {
		addEqClassID(eqClassIDs, eqID)
	}

	termIDs := make([]egraphTermID, 0, len(c.egraphTerms))
	for termID := range c.egraphTerms {
		termIDs = append(termIDs, termID)
	}
	slices.Sort(termIDs)
	for _, termID := range termIDs {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		outputEqID := c.findEqClassLocked(term.outputEqID)
		addEqClassID(eqClassIDs, outputEqID)
		inputProvenance := c.termInputProvenance[termID]
		if len(inputProvenance) != len(term.inputEqIDs) {
			c.egraphMu.RUnlock()
			return persistStateSnapshot{}, fmt.Errorf("persist term %d: input provenance len %d does not match input eq IDs len %d", termID, len(inputProvenance), len(term.inputEqIDs))
		}
		inputEqIDs := make([]eqClassID, len(term.inputEqIDs))
		copy(inputEqIDs, term.inputEqIDs)
		for i, inputEqID := range inputEqIDs {
			inputEqID = c.findEqClassLocked(inputEqID)
			inputEqIDs[i] = inputEqID
			addEqClassID(eqClassIDs, inputEqID)
			snapshot.termInputs = append(snapshot.termInputs, persistdb.MirrorTermInput{
				TermID:         int64(termID),
				Position:       int64(i),
				InputEqClassID: int64(inputEqID),
				ProvenanceKind: string(inputProvenance[i]),
			})
		}
		snapshot.terms = append(snapshot.terms, persistdb.MirrorTerm{
			ID:              int64(termID),
			SelfDigest:      term.selfDigest.String(),
			TermDigest:      calcEgraphTermDigest(term.selfDigest, inputEqIDs),
			OutputEqClassID: int64(outputEqID),
		})
	}

	resultIDs := make([]sharedResultID, 0, len(c.resultsByID))
	for resultID := range c.resultsByID {
		resultIDs = append(resultIDs, resultID)
	}
	slices.Sort(resultIDs)
	for _, resultID := range resultIDs {
		res := c.resultsByID[resultID]
		if res == nil {
			continue
		}

		canonicalID := c.resultCanonicalIDs[resultID]
		if canonicalID != nil {
			canonicalID = canonicalID.With()
		}

		depIDs := make([]sharedResultID, 0, len(res.deps))
		for depID := range res.deps {
			depIDs = append(depIDs, depID)
		}
		slices.Sort(depIDs)
		resultDeps := make([]persistdb.MirrorResultDep, 0, len(depIDs))
		for _, depID := range depIDs {
			resultDeps = append(resultDeps, persistdb.MirrorResultDep{
				ParentResultID: int64(resultID),
				DepResultID:    int64(depID),
			})
		}

		links := append([]PersistedSnapshotRefLink(nil), c.persistedSnapshotLinksForResultLocked(res)...)
		slices.SortFunc(links, func(a, b PersistedSnapshotRefLink) int {
			switch {
			case a.RefKey < b.RefKey:
				return -1
			case a.RefKey > b.RefKey:
				return 1
			case a.Role < b.Role:
				return -1
			case a.Role > b.Role:
				return 1
			case a.Slot < b.Slot:
				return -1
			case a.Slot > b.Slot:
				return 1
			default:
				return 0
			}
		})
		resultSnapshotLinks := make([]persistdb.MirrorResultSnapshotLink, 0, len(links))
		for _, link := range links {
			resultSnapshotLinks = append(resultSnapshotLinks, persistdb.MirrorResultSnapshotLink{
				ResultID: int64(resultID),
				RefKey:   link.RefKey,
				Role:     link.Role,
				Slot:     link.Slot,
			})
		}

		outputEqClasses := c.outputEqClassesForResultLocked(resultID)
		outputEqIDs := make([]eqClassID, 0, len(outputEqClasses))
		for outputEqID := range outputEqClasses {
			addEqClassID(eqClassIDs, outputEqID)
			outputEqIDs = append(outputEqIDs, outputEqID)
		}
		slices.Sort(outputEqIDs)
		for _, outputEqID := range outputEqIDs {
			snapshot.resultOutputEqClasses = append(snapshot.resultOutputEqClasses, persistdb.MirrorResultOutputEqClass{
				ResultID:  int64(resultID),
				EqClassID: int64(outputEqID),
			})
		}

		snapshot.results = append(snapshot.results, persistResultSnapshot{
			resultID:    resultID,
			canonicalID: canonicalID,
			shared: &sharedResult{
				self:                   res.self,
				objType:                res.objType,
				resultCallFrame:        res.resultCallFrame.clone(),
				hasValue:               res.hasValue,
				safeToPersistCache:     res.safeToPersistCache,
				depOfPersistedResult:   res.depOfPersistedResult,
				persistedEnvelope:      res.persistedEnvelope,
				persistedSnapshotLinks: slices.Clone(res.persistedSnapshotLinks),
				outputEffectIDs:        slices.Clone(res.outputEffectIDs),
				expiresAtUnix:          res.expiresAtUnix,
				createdAtUnixNano:      res.createdAtUnixNano,
				lastUsedAtUnixNano:     res.lastUsedAtUnixNano,
				sizeEstimateBytes:      res.sizeEstimateBytes,
				usageIdentity:          res.usageIdentity,
				description:            res.description,
				recordType:             res.recordType,
			},
			resultDeps:          resultDeps,
			resultSnapshotLinks: resultSnapshotLinks,
		})
	}

	eqIDs := make([]eqClassID, 0, len(eqClassIDs))
	for eqID := range eqClassIDs {
		eqIDs = append(eqIDs, eqID)
	}
	slices.Sort(eqIDs)
	for _, eqID := range eqIDs {
		snapshot.eqClasses = append(snapshot.eqClasses, persistdb.MirrorEqClass{ID: int64(eqID)})

		digestRows := make(map[string]persistdb.MirrorEqClassDigest, len(c.eqClassToDigests[eqID]))
		for dig := range c.eqClassToDigests[eqID] {
			if dig == "" {
				continue
			}
			digestRows[dig+"\x00"] = persistdb.MirrorEqClassDigest{
				EqClassID: int64(eqID),
				Digest:    dig,
				Label:     "",
			}
		}
		for extra := range c.eqClassExtraDigests[eqID] {
			if extra.Digest == "" {
				continue
			}
			dig := extra.Digest.String()
			digestRows[dig+"\x00"] = persistdb.MirrorEqClassDigest{
				EqClassID: int64(eqID),
				Digest:    dig,
				Label:     "",
			}
			digestRows[dig+"\x01"+extra.Label] = persistdb.MirrorEqClassDigest{
				EqClassID: int64(eqID),
				Digest:    dig,
				Label:     extra.Label,
			}
		}
		rowKeys := make([]string, 0, len(digestRows))
		for key := range digestRows {
			rowKeys = append(rowKeys, key)
		}
		slices.Sort(rowKeys)
		for _, key := range rowKeys {
			snapshot.eqClassDigests = append(snapshot.eqClassDigests, digestRows[key])
		}
	}

	c.egraphMu.RUnlock()

	for i := range snapshot.results {
		resultSnapshot := &snapshot.results[i]
		if resultSnapshot.canonicalID == nil {
			return persistStateSnapshot{}, fmt.Errorf("persist result %d: missing canonical ID", resultSnapshot.resultID)
		}

		env, err := c.persistResultEnvelope(ctx, resultSnapshot.shared, resultSnapshot.canonicalID)
		switch {
		case errors.Is(err, ErrPersistStateNotReady):
			return persistStateSnapshot{}, err
		case err != nil:
			return persistStateSnapshot{}, fmt.Errorf("persist result %d envelope: %w", resultSnapshot.resultID, err)
		}

		payload, err := json.Marshal(env)
		if err != nil {
			return persistStateSnapshot{}, fmt.Errorf("persist result %d payload JSON: %w", resultSnapshot.resultID, err)
		}
		outputEffectIDs, err := json.Marshal(resultSnapshot.shared.outputEffectIDs)
		if err != nil {
			return persistStateSnapshot{}, fmt.Errorf("persist result %d output effect IDs: %w", resultSnapshot.resultID, err)
		}
		canonicalID, err := resultSnapshot.canonicalID.Encode()
		if err != nil {
			return persistStateSnapshot{}, fmt.Errorf("persist result %d canonical ID: %w", resultSnapshot.resultID, err)
		}

		resultSnapshot.row = persistdb.MirrorResult{
			ID:                   int64(resultSnapshot.resultID),
			CanonicalID:          canonicalID,
			SelfPayload:          payload,
			OutputEffectIDs:      string(outputEffectIDs),
			SafeToPersistCache:   resultSnapshot.shared.safeToPersistCache,
			DepOfPersistedResult: resultSnapshot.shared.depOfPersistedResult,
			ExpiresAtUnix:        resultSnapshot.shared.expiresAtUnix,
			CreatedAtUnixNano:    resultSnapshot.shared.createdAtUnixNano,
			LastUsedAtUnixNano:   resultSnapshot.shared.lastUsedAtUnixNano,
			SizeEstimateBytes:    resultSnapshot.shared.sizeEstimateBytes,
			UsageIdentity:        resultSnapshot.shared.usageIdentity,
			RecordType:           resultSnapshot.shared.recordType,
			Description:          resultSnapshot.shared.description,
		}
	}
	return snapshot, nil
}

func (c *cache) applyPersistStateSnapshot(ctx context.Context, snapshot persistStateSnapshot) error {
	if c.sqlDB == nil || c.pdb == nil {
		return nil
	}

	tx, err := c.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin persistence mirror tx: %w", err)
	}
	q := c.pdb.WithTx(tx)
	if err := q.ClearMirrorState(ctx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clear mirror state: %w", err)
	}

	for _, row := range snapshot.eqClasses {
		if err := q.InsertMirrorEqClass(ctx, row); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert eq_class %d: %w", row.ID, err)
		}
	}
	for _, row := range snapshot.eqClassDigests {
		if err := q.InsertMirrorEqClassDigest(ctx, row); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert eq_class_digest (%d,%s,%s): %w", row.EqClassID, row.Digest, row.Label, err)
		}
	}
	for _, result := range snapshot.results {
		if err := q.InsertMirrorResult(ctx, result.row); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert result %d: %w", result.resultID, err)
		}
	}
	for _, row := range snapshot.terms {
		if err := q.InsertMirrorTerm(ctx, row); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert term %d: %w", row.ID, err)
		}
	}
	for _, row := range snapshot.termInputs {
		if err := q.InsertMirrorTermInput(ctx, row); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert term_input (%d,%d): %w", row.TermID, row.Position, err)
		}
	}
	for _, row := range snapshot.resultOutputEqClasses {
		if err := q.InsertMirrorResultOutputEqClass(ctx, row); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert result_output_eq_class (%d,%d): %w", row.ResultID, row.EqClassID, err)
		}
	}
	for _, result := range snapshot.results {
		for _, row := range result.resultDeps {
			if err := q.InsertMirrorResultDep(ctx, row); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("insert result_dep (%d,%d): %w", row.ParentResultID, row.DepResultID, err)
			}
		}
		for _, row := range result.resultSnapshotLinks {
			if err := q.InsertMirrorResultSnapshotLink(ctx, row); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("insert result_snapshot_link (%d,%s,%s,%s): %w", row.ResultID, row.RefKey, row.Role, row.Slot, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit persistence mirror tx: %w", err)
	}
	return nil
}

func (c *cache) persistResultEnvelope(ctx context.Context, res *sharedResult, canonicalID *call.ID) (PersistedResultEnvelope, error) {
	if res != nil && res.persistedEnvelope != nil {
		return *res.persistedEnvelope, nil
	}
	if res == nil || !res.hasValue {
		return PersistedResultEnvelope{
			Version: 1,
			Kind:    persistedResultKindNull,
		}, nil
	}
	if canonicalID == nil {
		return PersistedResultEnvelope{}, fmt.Errorf("result has nil canonical ID and no persisted envelope")
	}
	typedRes := Result[Typed]{
		shared: res,
		id:     canonicalID,
	}
	var anyRes AnyResult = typedRes
	if res.objType != nil {
		objRes, err := res.objType.New(typedRes)
		if err != nil {
			return PersistedResultEnvelope{}, fmt.Errorf("reconstruct object result: %w", err)
		}
		anyRes = objRes
	}
	persistCtx := context.WithoutCancel(ctx)
	persistCtx = ContextWithID(persistCtx, canonicalID)
	return DefaultPersistedSelfCodec.EncodeResult(persistCtx, c, anyRes)
}

func (c *cache) persistedSnapshotLinksForResultLocked(res *sharedResult) []PersistedSnapshotRefLink {
	if res == nil {
		return nil
	}
	typedLinks := persistedSnapshotLinksFromTyped(res.self)
	if len(typedLinks) > 0 {
		return typedLinks
	}
	if len(res.persistedSnapshotLinks) == 0 {
		return nil
	}
	links := make([]PersistedSnapshotRefLink, len(res.persistedSnapshotLinks))
	copy(links, res.persistedSnapshotLinks)
	return links
}
