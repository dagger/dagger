package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
)

func (c *cache) importPersistedState(ctx context.Context) error {
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
	resultDepRows, err := c.pdb.ListMirrorResultDeps(ctx)
	if err != nil {
		return fmt.Errorf("list mirror result_deps: %w", err)
	}
	resultSnapshotRows, err := c.pdb.ListMirrorResultSnapshotLinks(ctx)
	if err != nil {
		return fmt.Errorf("list mirror result_snapshot_links: %w", err)
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

			if row.CallFrameJSON == "" {
				return fmt.Errorf("import result %d: empty call_frame_json", resultID)
			}
			var frame ResultCall
			if err := json.Unmarshal([]byte(row.CallFrameJSON), &frame); err != nil {
				return fmt.Errorf("import result %d call_frame_json: %w", resultID, err)
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

			var outputEffects []string
			if row.OutputEffectIDs != "" {
				if err := json.Unmarshal([]byte(row.OutputEffectIDs), &outputEffects); err != nil {
					return fmt.Errorf("import result %d output effect IDs: %w", resultID, err)
				}
			}

			res := &sharedResult{
				cache:                c,
				id:                   resultID,
				resultCall:           &frame,
				safeToPersistCache:   row.SafeToPersistCache,
				depOfPersistedResult: row.DepOfPersistedResult,
				outputEffectIDs:      outputEffects,
				expiresAtUnix:        row.ExpiresAtUnix,
				createdAtUnixNano:    row.CreatedAtUnixNano,
				lastUsedAtUnixNano:   row.LastUsedAtUnixNano,
				sizeEstimateBytes:    row.SizeEstimateBytes,
				usageIdentity:        row.UsageIdentity,
				description:          row.Description,
				recordType:           row.RecordType,
				persistedEnvelope:    &env,
			}
			res.resultCall.bindCache(c)

			if env.Kind == persistedResultKindNull {
				res.hasValue = true
				res.persistedEnvelope = nil
				c.tracePersistedPayloadImportedEager(ctx, importRunID, resultID, "", "nil")
			} else {
				eagerDecodeResultIDs = append(eagerDecodeResultIDs, resultID)
			}
			if res.usageIdentity == "" {
				if usageIdentity, ok := cacheUsageIdentity(res); ok {
					res.usageIdentity = usageIdentity
				}
			}

			c.resultsByID[resultID] = res
			c.traceImportResultLoaded(ctx, importRunID, resultID, row.CallFrameJSON)
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
				digestTerms = make(map[egraphTermID]struct{})
				c.egraphTermsByTermDigest[term.termDigest] = digestTerms
			}
			digestTerms[termID] = struct{}{}
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
			c.traceImportResultDepLoaded(ctx, importRunID, parentID, depID)
			c.traceExplicitDepAdded(ctx, parentID, depID, "import")
		}

		for _, row := range resultSnapshotRows {
			resultID := sharedResultID(row.ResultID)
			res := c.resultsByID[resultID]
			if res == nil {
				return fmt.Errorf("import result_snapshot_link: missing result %d", row.ResultID)
			}
			res.persistedSnapshotLinks = append(res.persistedSnapshotLinks, PersistedSnapshotRefLink{
				RefKey: row.RefKey,
				Role:   row.Role,
				Slot:   row.Slot,
			})
			c.traceImportResultSnapshotLinkLoaded(ctx, importRunID, resultID, row.RefKey, row.Role, row.Slot)
		}

		for resultID := range c.resultsByID {
			outputEqClasses := c.outputEqClassesForResultLocked(resultID)
			for outputEqID := range outputEqClasses {
				for dig := range c.eqClassToDigests[outputEqID] {
					set := c.egraphResultsByDigest[dig]
					if set == nil {
						set = make(map[sharedResultID]struct{})
						c.egraphResultsByDigest[dig] = set
					}
					set[resultID] = struct{}{}
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
		if res == nil || res.hasValue || res.persistedEnvelope == nil {
			continue
		}
		call := c.resultCallByResultID(resultID)
		if call == nil {
			continue
		}
		decodeCtx := ContextWithCall(ctx, call)
		if decoded, err := DefaultPersistedSelfCodec.DecodeResult(decodeCtx, nil, uint64(resultID), call, *res.persistedEnvelope); err == nil && decoded != nil {
			res.self = decoded.Unwrap()
			res.hasValue = true
			if objRes, ok := decoded.(AnyObjectResult); ok {
				res.objType = objRes.ObjectType()
			}
			setTypedPersistedResultID(res.self, resultID)
			res.persistedEnvelope = nil
			c.tracePersistedPayloadImportedEager(ctx, importRunID, resultID, "", "materialized")
		}
	}

	for _, resultID := range eagerDecodeResultIDs {
		res := c.resultsByID[resultID]
		if res == nil || res.persistedEnvelope == nil || res.hasValue {
			continue
		}
		c.tracePersistedPayloadImportedLazy(ctx, importRunID, resultID, "", res.persistedEnvelope.Kind, res.persistedEnvelope.TypeName)
	}

	return nil
}

func normalizeImportedDigest(raw string) digest.Digest {
	if raw == "" {
		return ""
	}
	return digest.Digest(raw)
}

func (c *cache) ensurePersistedHitValueLoaded(ctx context.Context, dag *Server, hit AnyResult) (AnyResult, error) {
	if hit == nil {
		return nil, nil
	}
	res := hit.cacheSharedResult()
	if res == nil {
		return hit, nil
	}

	c.egraphMu.RLock()
	hasValue := res.hasValue
	env := res.persistedEnvelope
	c.egraphMu.RUnlock()
	if hasValue || env == nil {
		return hit, nil
	}

	call := c.resultCallByResultID(res.id)
	if call == nil {
		return hit, nil
	}
	decodeCtx := ContextWithCall(ctx, call)
	decoded, err := DefaultPersistedSelfCodec.DecodeResult(decodeCtx, dag, uint64(res.id), call, *env)
	if err != nil {
		c.tracePersistedPayloadDecodeFailed(ctx, res, env, err)
		return nil, fmt.Errorf("decode persisted hit payload: %w", err)
	}

	c.egraphMu.Lock()
	if !res.hasValue && res.persistedEnvelope != nil {
		res.self = decoded.Unwrap()
		res.hasValue = true
		if objRes, ok := decoded.(AnyObjectResult); ok {
			res.objType = objRes.ObjectType()
		}
		setTypedPersistedResultID(res.self, res.id)
		res.persistedEnvelope = nil
		c.tracePersistedPayloadDecoded(ctx, res, env)
	}
	objType := res.objType
	c.egraphMu.Unlock()

	ret := Result[Typed]{
		shared:   res,
		hitCache: hit.HitCache(),
	}
	if objType == nil {
		return ret, nil
	}
	objRes, err := objType.New(ret)
	if err != nil {
		return nil, fmt.Errorf("reconstruct object result from persisted hit payload: %w", err)
	}
	return objRes, nil
}
