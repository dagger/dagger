package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql/call"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
)

func (c *Cache) importPersistedState(ctx context.Context) error {
	if c.pdb == nil {
		return nil
	}
	importRunID := c.nextImportRunID()

	resultRows, err := c.pdb.ListMirrorResults(ctx)
	if err != nil {
		return fmt.Errorf("list mirror results: %w", err)
	}
	eqClassRows, err := c.pdb.ListMirrorEqClasses(ctx)
	if err != nil {
		return fmt.Errorf("list mirror eq_classes: %w", err)
	}
	eqClassDigestRows, err := c.pdb.ListMirrorEqClassDigests(ctx)
	if err != nil {
		return fmt.Errorf("list mirror eq_class_digests: %w", err)
	}
	termRows, err := c.pdb.ListMirrorTerms(ctx)
	if err != nil {
		return fmt.Errorf("list mirror terms: %w", err)
	}
	termInputRows, err := c.pdb.ListMirrorTermInputs(ctx)
	if err != nil {
		return fmt.Errorf("list mirror term_inputs: %w", err)
	}
	resultOutputEqClassRows, err := c.pdb.ListMirrorResultOutputEqClasses(ctx)
	if err != nil {
		return fmt.Errorf("list mirror result_output_eq_classes: %w", err)
	}
	persistedEdgeRows, err := c.pdb.ListMirrorPersistedEdges(ctx)
	if err != nil {
		return fmt.Errorf("list mirror persisted_edges: %w", err)
	}
	resultDepRows, err := c.pdb.ListMirrorResultDeps(ctx)
	if err != nil {
		return fmt.Errorf("list mirror result_deps: %w", err)
	}
	resultSnapshotRows, err := c.pdb.ListMirrorResultSnapshotLinks(ctx)
	if err != nil {
		return fmt.Errorf("list mirror result_snapshot_links: %w", err)
	}
	snapshotContentRows, err := c.pdb.ListMirrorSnapshotContentLinks(ctx)
	if err != nil {
		return fmt.Errorf("list mirror snapshot_content_links: %w", err)
	}
	importedLayerBlobRows, err := c.pdb.ListMirrorImportedLayerBlobIndex(ctx)
	if err != nil {
		return fmt.Errorf("list mirror imported_layer_blob_index: %w", err)
	}
	importedLayerDiffRows, err := c.pdb.ListMirrorImportedLayerDiffIndex(ctx)
	if err != nil {
		return fmt.Errorf("list mirror imported_layer_diff_index: %w", err)
	}

	if len(resultRows) == 0 && len(eqClassRows) == 0 && len(termRows) == 0 {
		return nil
	}

	var eagerDecodeResultIDs []sharedResultID

	c.egraphMu.Lock()
	importErr := func() error {
		c.initEgraphLocked()

		var maxEqClassID eqClassID
		for _, row := range eqClassRows {
			eqID := eqClassID(row.ID)
			if eqID == 0 {
				return fmt.Errorf("import eq_class: zero ID")
			}
			if eqID > maxEqClassID {
				maxEqClassID = eqID
			}
		}
		c.egraphParents = make([]eqClassID, maxEqClassID+1)
		c.egraphRanks = make([]uint8, maxEqClassID+1)
		for _, row := range eqClassRows {
			eqID := eqClassID(row.ID)
			c.egraphParents[eqID] = eqID
			if c.eqClassToDigests[eqID] == nil {
				c.eqClassToDigests[eqID] = make(map[string]struct{})
			}
		}

		for _, row := range eqClassDigestRows {
			eqID := eqClassID(row.EqClassID)
			if eqID == 0 {
				return fmt.Errorf("import eq_class_digest %q: zero eq_class_id", row.Digest)
			}
			if c.egraphParents[eqID] == 0 {
				return fmt.Errorf("import eq_class_digest %q: missing eq_class %d", row.Digest, eqID)
			}
			if row.Digest == "" {
				return fmt.Errorf("import eq_class_digest: empty digest for eq_class %d", eqID)
			}
			c.egraphDigestToClass[row.Digest] = eqID
			digests := c.eqClassToDigests[eqID]
			if digests == nil {
				digests = make(map[string]struct{})
				c.eqClassToDigests[eqID] = digests
			}
			digests[row.Digest] = struct{}{}
			if row.Label != "" {
				extras := c.eqClassExtraDigests[eqID]
				if extras == nil {
					extras = make(map[call.ExtraDigest]struct{})
					c.eqClassExtraDigests[eqID] = extras
				}
				extras[call.ExtraDigest{
					Digest: digest.Digest(row.Digest),
					Label:  row.Label,
				}] = struct{}{}
			}
		}

		var maxResultID sharedResultID
		for _, row := range resultRows {
			resultID := sharedResultID(row.ID)
			if resultID == 0 {
				return fmt.Errorf("import result: zero ID")
			}
			if resultID > maxResultID {
				maxResultID = resultID
			}

			var env PersistedResultEnvelope
			if len(row.SelfPayload) > 0 {
				if err := json.Unmarshal(row.SelfPayload, &env); err != nil {
					return fmt.Errorf("import result %d self payload: %w", resultID, err)
				}
			} else {
				env = PersistedResultEnvelope{
					Version: 1,
					Kind:    persistedResultKindNull,
				}
			}
			if env.Kind == "" {
				return fmt.Errorf("import result %d: empty self payload kind", resultID)
			}

			var frame *ResultCall
			if row.CallFrameJSON != "" {
				var decoded ResultCall
				if err := json.Unmarshal([]byte(row.CallFrameJSON), &decoded); err != nil {
					return fmt.Errorf("import result %d call_frame_json: %w", resultID, err)
				}
				frame = &decoded
			} else {
				return fmt.Errorf("import result %d: empty call_frame_json", resultID)
			}

			res := &sharedResult{
				id:                    resultID,
				isObject:              env.Kind == persistedResultKindObject,
				sessionResourceHandle: env.SessionResourceHandle,
				expiresAtUnix:         row.ExpiresAtUnix,
				createdAtUnixNano:     row.CreatedAtUnixNano,
				lastUsedAtUnixNano:    row.LastUsedAtUnixNano,
				description:           row.Description,
				recordType:            row.RecordType,
				persistedEnvelope:     &env,
			}
			if frame != nil {
				res.storeResultCall(frame)
				c.traceResultCallFrameUpdated(ctx, res, "import_persisted_result", nil, frame)
			}

			if env.Kind == persistedResultKindNull {
				res.hasValue = true
				res.persistedEnvelope = nil
				c.tracePersistedPayloadImportedEager(ctx, importRunID, resultID, "", "nil")
			} else {
				eagerDecodeResultIDs = append(eagerDecodeResultIDs, resultID)
			}
			c.resultsByID[resultID] = res
			c.traceImportResultLoaded(ctx, importRunID, resultID, row.CallFrameJSON)
		}

		for _, row := range persistedEdgeRows {
			resultID := sharedResultID(row.ResultID)
			if resultID == 0 {
				return fmt.Errorf("import persisted_edge: zero result ID")
			}
			res := c.resultsByID[resultID]
			if res == nil {
				return fmt.Errorf("import persisted_edge %d: missing result", resultID)
			}
			if c.persistedEdgesByResult == nil {
				c.persistedEdgesByResult = make(map[sharedResultID]persistedEdge)
			}
			edge := persistedEdge{
				resultID:          resultID,
				createdAtUnixNano: row.CreatedAtUnixNano,
				expiresAtUnix:     row.ExpiresAtUnix,
				unpruneable:       row.Unpruneable,
			}
			if edge.unpruneable {
				edge.expiresAtUnix = 0
				res.expiresAtUnix = 0
			}
			c.persistedEdgesByResult[resultID] = edge
			c.incrementIncomingOwnershipLocked(ctx, res)
		}

		type importTermInput struct {
			position       int
			inputEqClassID eqClassID
			provenanceKind egraphInputProvenanceKind
		}
		inputsByTermID := make(map[egraphTermID][]importTermInput, len(termRows))
		for _, row := range termInputRows {
			termID := egraphTermID(row.TermID)
			if termID == 0 {
				return fmt.Errorf("import term_input: zero term_id")
			}
			provenance := egraphInputProvenanceKind(row.ProvenanceKind)
			switch provenance {
			case egraphInputProvenanceKindResult, egraphInputProvenanceKindDigest:
			default:
				return fmt.Errorf("import term_input %d/%d: unsupported provenance %q", row.TermID, row.Position, row.ProvenanceKind)
			}
			inputsByTermID[termID] = append(inputsByTermID[termID], importTermInput{
				position:       int(row.Position),
				inputEqClassID: eqClassID(row.InputEqClassID),
				provenanceKind: provenance,
			})
		}

		var maxTermID egraphTermID
		for _, row := range termRows {
			termID := egraphTermID(row.ID)
			if termID == 0 {
				return fmt.Errorf("import term: zero ID")
			}
			if termID > maxTermID {
				maxTermID = termID
			}

			inputs := inputsByTermID[termID]
			slices.SortFunc(inputs, func(a, b importTermInput) int {
				switch {
				case a.position < b.position:
					return -1
				case a.position > b.position:
					return 1
				default:
					return 0
				}
			})
			inputEqIDs := make([]eqClassID, 0, len(inputs))
			inputProvenance := make([]egraphInputProvenanceKind, 0, len(inputs))
			for idx, input := range inputs {
				if input.position != idx {
					return fmt.Errorf("import term %d inputs: missing position %d", termID, idx)
				}
				inputEqIDs = append(inputEqIDs, c.findEqClassLocked(input.inputEqClassID))
				inputProvenance = append(inputProvenance, input.provenanceKind)
			}

			selfDigest := normalizeImportedDigest(row.SelfDigest)
			outputEqID := c.findEqClassLocked(eqClassID(row.OutputEqClassID))
			termDigest := calcEgraphTermDigest(selfDigest, inputEqIDs)
			if row.TermDigest != "" && row.TermDigest != termDigest {
				return fmt.Errorf("import term %d digest mismatch: stored=%s rebuilt=%s", termID, row.TermDigest, termDigest)
			}

			term := newEgraphTerm(termID, selfDigest, inputEqIDs, outputEqID)
			c.egraphTerms[termID] = term
			c.termInputProvenance[termID] = inputProvenance
			digestTerms := c.egraphTermsByTermDigest[term.termDigest]
			if digestTerms == nil {
				digestTerms = newEgraphTermIDSet()
				c.egraphTermsByTermDigest[term.termDigest] = digestTerms
			}
			digestTerms.Insert(termID)
			for _, inputEqID := range inputEqIDs {
				if inputEqID == 0 {
					continue
				}
				classTerms := c.inputEqClassToTerms[inputEqID]
				if classTerms == nil {
					classTerms = make(map[egraphTermID]struct{})
					c.inputEqClassToTerms[inputEqID] = classTerms
				}
				classTerms[termID] = struct{}{}
			}
			outputTerms := c.outputEqClassToTerms[outputEqID]
			if outputTerms == nil {
				outputTerms = make(map[egraphTermID]struct{})
				c.outputEqClassToTerms[outputEqID] = outputTerms
			}
			outputTerms[termID] = struct{}{}
			c.traceTermCreated(ctx, "import", importRunID, term)
		}

		for _, row := range resultOutputEqClassRows {
			resultID := sharedResultID(row.ResultID)
			res := c.resultsByID[resultID]
			if res == nil {
				return fmt.Errorf("import result_output_eq_class: missing result %d", row.ResultID)
			}
			outputEqID := c.findEqClassLocked(eqClassID(row.EqClassID))
			if outputEqID == 0 {
				return fmt.Errorf("import result_output_eq_class: missing eq_class %d", row.EqClassID)
			}
			outputEqClasses := c.resultOutputEqClasses[resultID]
			if outputEqClasses == nil {
				outputEqClasses = make(map[eqClassID]struct{})
				c.resultOutputEqClasses[resultID] = outputEqClasses
			}
			outputEqClasses[outputEqID] = struct{}{}
		}

		for _, row := range resultDepRows {
			parentID := sharedResultID(row.ParentResultID)
			parent := c.resultsByID[parentID]
			if parent == nil {
				return fmt.Errorf("import result_dep: missing parent result %d", row.ParentResultID)
			}
			depID := sharedResultID(row.DepResultID)
			if c.resultsByID[depID] == nil {
				return fmt.Errorf("import result_dep: missing dep result %d", row.DepResultID)
			}
			if parent.deps == nil {
				parent.deps = make(map[sharedResultID]struct{})
			}
			parent.deps[depID] = struct{}{}
			c.incrementIncomingOwnershipLocked(ctx, c.resultsByID[depID])
			c.traceImportResultDepLoaded(ctx, importRunID, parentID, depID)
			c.traceExplicitDepAdded(ctx, parentID, depID, "import")
		}

		for _, row := range resultSnapshotRows {
			resultID := sharedResultID(row.ResultID)
			res := c.resultsByID[resultID]
			if res == nil {
				return fmt.Errorf("import result_snapshot_link: missing result %d", row.ResultID)
			}
			res.snapshotOwnerLinks = append(res.snapshotOwnerLinks, PersistedSnapshotRefLink{
				RefKey: row.RefKey,
				Role:   row.Role,
				Slot:   row.Slot,
			})
			c.traceImportResultSnapshotLinkLoaded(ctx, importRunID, resultID, row.RefKey, row.Role, row.Slot)
		}

		for _, res := range c.resultsByID {
			res.onRelease = joinOnRelease(c.resultSnapshotLeaseCleanup(res), res.onRelease)
		}

		for _, res := range c.resultsByID {
			if err := c.recomputeRequiredSessionResourcesLocked(res); err != nil {
				return fmt.Errorf("recompute imported required session resources for result %d: %w", res.id, err)
			}
		}

		for resultID := range c.resultsByID {
			outputEqClasses := c.outputEqClassesForResultLocked(resultID)
			for outputEqID := range outputEqClasses {
				for dig := range c.eqClassToDigests[outputEqID] {
					set := c.egraphResultsByDigest[dig]
					if set == nil {
						set = newSharedResultIDSet()
						c.egraphResultsByDigest[dig] = set
					}
					set.Insert(resultID)
				}
			}
		}

		c.nextSharedResultID = maxResultID + 1
		c.nextEgraphTermID = maxTermID + 1
		c.nextEgraphClassID = maxEqClassID + 1
		if c.nextSharedResultID == 0 {
			c.nextSharedResultID = 1
		}
		if c.nextEgraphTermID == 0 {
			c.nextEgraphTermID = 1
		}
		if c.nextEgraphClassID == 0 {
			c.nextEgraphClassID = 1
		}

		return nil
	}()
	c.egraphMu.Unlock()
	if importErr != nil {
		return importErr
	}

	for _, resultID := range eagerDecodeResultIDs {
		res := c.resultsByID[resultID]
		state := res.loadPayloadState()
		if res == nil || state.hasValue || state.persistedEnvelope == nil {
			continue
		}
		call := res.loadResultCall()
		if call == nil {
			continue
		}
		decodeCtx := ContextWithCall(ctx, call)
		if decoded, err := DefaultPersistedSelfCodec.DecodeResult(decodeCtx, nil, uint64(resultID), call, *state.persistedEnvelope); err == nil && decoded != nil {
			res.payloadMu.Lock()
			if !res.hasValue && res.persistedEnvelope != nil {
				res.self = decoded.Unwrap()
				res.hasValue = true
				decodedShared := decoded.cacheSharedResult()
				if decodedShared != nil {
					res.sessionResourceHandle = decodedShared.sessionResourceHandle
					if decodedShared.requiredSessionResources != nil {
						res.requiredSessionResources = decodedShared.requiredSessionResources.Copy()
					} else if decodedShared.sessionResourceHandle == "" {
						res.requiredSessionResources = nil
					}
				}
				res.persistedEnvelope = nil
			}
			res.payloadMu.Unlock()
			if onReleaser, ok := UnwrapAs[OnReleaser](decoded); ok {
				res.onRelease = joinOnRelease(c.resultSnapshotLeaseCleanup(res), onReleaser.OnRelease)
			}
			if err := c.syncResultSnapshotLeases(ctx, res); err != nil {
				return err
			}
			c.tracePersistedPayloadImportedEager(ctx, importRunID, resultID, "", "materialized")
		}
	}

	for _, resultID := range eagerDecodeResultIDs {
		res := c.resultsByID[resultID]
		state := res.loadPayloadState()
		if res == nil || state.persistedEnvelope == nil || state.hasValue {
			continue
		}
		c.tracePersistedPayloadImportedLazy(ctx, importRunID, resultID, "", state.persistedEnvelope.Kind, state.persistedEnvelope.TypeName)
	}

	if c.snapshotManager != nil {
		rows := bkcache.PersistentMetadataRows{
			SnapshotContent: make([]bkcache.SnapshotContentRow, 0, len(snapshotContentRows)),
			ImportedByBlob:  make([]bkcache.ImportedLayerBlobRow, 0, len(importedLayerBlobRows)),
			ImportedByDiff:  make([]bkcache.ImportedLayerDiffRow, 0, len(importedLayerDiffRows)),
		}
		for _, row := range snapshotContentRows {
			rows.SnapshotContent = append(rows.SnapshotContent, bkcache.SnapshotContentRow{
				SnapshotID: row.SnapshotID,
				Digest:     normalizeImportedDigest(row.Digest),
			})
		}
		for _, row := range importedLayerBlobRows {
			rows.ImportedByBlob = append(rows.ImportedByBlob, bkcache.ImportedLayerBlobRow{
				ParentSnapshotID: row.ParentSnapshotID,
				BlobDigest:       normalizeImportedDigest(row.BlobDigest),
				SnapshotID:       row.SnapshotID,
			})
		}
		for _, row := range importedLayerDiffRows {
			rows.ImportedByDiff = append(rows.ImportedByDiff, bkcache.ImportedLayerDiffRow{
				ParentSnapshotID: row.ParentSnapshotID,
				DiffID:           normalizeImportedDigest(row.DiffID),
				SnapshotID:       row.SnapshotID,
			})
		}
		if err := c.snapshotManager.LoadPersistentMetadata(rows); err != nil {
			return fmt.Errorf("hydrate snapshot metadata: %w", err)
		}

		desiredLeaseIDs, err := c.desiredImportedOwnerLeaseIDs(ctx)
		if err != nil {
			return fmt.Errorf("compute desired imported owner leases: %w", err)
		}
		c.egraphMu.RLock()
		results := make([]*sharedResult, 0, len(c.resultsByID))
		for _, res := range c.resultsByID {
			if res != nil {
				results = append(results, res)
			}
		}
		c.egraphMu.RUnlock()
		for _, res := range results {
			links, ok := c.authoritativeSnapshotLinksForResult(res)
			if !ok {
				continue
			}
			seen := make(map[snapshotOwnerKey]struct{}, len(links))
			for _, link := range links {
				key := snapshotOwnerKey{Role: link.Role, Slot: link.Slot}
				if _, alreadySeen := seen[key]; alreadySeen {
					continue
				}
				seen[key] = struct{}{}
				if err := c.snapshotManager.AttachLease(
					ctx,
					resultSnapshotLeaseID(res.id, link.Role, link.Slot),
					link.RefKey,
				); err != nil {
					return fmt.Errorf("attach imported result %d owner lease %q: %w", res.id, key.Role, err)
				}
			}
		}
		if err := c.snapshotManager.DeleteStaleDaggerOwnerLeases(ctx, desiredLeaseIDs); err != nil {
			return fmt.Errorf("delete stale owner leases: %w", err)
		}
	}

	return nil
}

func normalizeImportedDigest(raw string) digest.Digest {
	if raw == "" {
		return ""
	}
	return digest.Digest(raw)
}

func resolverServer(resolver TypeResolver) *Server {
	if resolver == nil {
		return nil
	}
	dag, ok := resolver.(*Server)
	if !ok {
		return nil
	}
	return dag
}

func (c *Cache) ensurePersistedHitValueLoaded(ctx context.Context, resolver TypeResolver, hit AnyResult) (AnyResult, error) {
	if resolver == nil {
		return nil, fmt.Errorf("ensure persisted hit value loaded: type resolver is nil")
	}
	if hit == nil {
		return nil, nil
	}
	res := hit.cacheSharedResult()
	if res == nil {
		return hit, nil
	}

	state := res.loadPayloadState()
	if state.hasValue || state.persistedEnvelope == nil {
		if !state.isObject {
			c.registerLazyEvaluation(res, hit)
			return hit, nil
		}
		objRes, err := wrapSharedResultWithResolver(res, hit.HitCache(), resolver)
		if err != nil {
			return nil, fmt.Errorf("reconstruct object result from cache hit payload: %w", err)
		}
		c.registerLazyEvaluation(res, objRes)
		return objRes, nil
	}

	call := res.loadResultCall()
	dag := resolverServer(resolver)
	if call == nil {
		return hit, nil
	}
	decodeCtx := ContextWithCall(ctx, call)
	if dag == nil {
		return nil, fmt.Errorf("decode persisted hit payload: type resolver %T does not provide dagql server", resolver)
	}
	decoded, err := DefaultPersistedSelfCodec.DecodeResult(decodeCtx, dag, uint64(res.id), call, *state.persistedEnvelope)
	if err != nil {
		c.tracePersistedPayloadDecodeFailed(ctx, res, state.persistedEnvelope, err)
		return nil, fmt.Errorf("decode persisted hit payload: %w", err)
	}

	res.payloadMu.Lock()
	if !res.hasValue && res.persistedEnvelope != nil {
		res.self = decoded.Unwrap()
		res.hasValue = true
		decodedShared := decoded.cacheSharedResult()
		if decodedShared != nil {
			res.sessionResourceHandle = decodedShared.sessionResourceHandle
			if decodedShared.requiredSessionResources != nil {
				res.requiredSessionResources = decodedShared.requiredSessionResources.Copy()
			} else if decodedShared.sessionResourceHandle == "" {
				res.requiredSessionResources = nil
			}
		}
		res.persistedEnvelope = nil
		c.tracePersistedPayloadDecoded(ctx, res, state.persistedEnvelope)
	}
	res.payloadMu.Unlock()
	if onReleaser, ok := UnwrapAs[OnReleaser](decoded); ok {
		res.onRelease = joinOnRelease(c.resultSnapshotLeaseCleanup(res), onReleaser.OnRelease)
	}
	if err := c.syncResultSnapshotLeases(ctx, res); err != nil {
		return nil, fmt.Errorf("sync persisted hit owner leases: %w", err)
	}
	wrapped, err := wrapSharedResultWithResolver(res, hit.HitCache(), resolver)
	if err != nil {
		return nil, err
	}
	c.registerLazyEvaluation(res, wrapped)
	return wrapped, nil
}
