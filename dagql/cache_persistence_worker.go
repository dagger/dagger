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

func collectSnapshotExtraDigestsLocked(
	eqClassExtraDigests map[eqClassID]map[call.ExtraDigest]struct{},
	outputEqClasses map[eqClassID]struct{},
) []call.ExtraDigest {
	if len(outputEqClasses) == 0 {
		return nil
	}
	seen := make(map[call.ExtraDigest]struct{})
	extras := make([]call.ExtraDigest, 0)
	for outputEqID := range outputEqClasses {
		for extra := range eqClassExtraDigests[outputEqID] {
			if extra.Digest == "" {
				continue
			}
			if _, ok := seen[extra]; ok {
				continue
			}
			seen[extra] = struct{}{}
			extras = append(extras, extra)
		}
	}
	slices.SortFunc(extras, func(a, b call.ExtraDigest) int {
		switch {
		case a.Label < b.Label:
			return -1
		case a.Label > b.Label:
			return 1
		case a.Digest < b.Digest:
			return -1
		case a.Digest > b.Digest:
			return 1
		default:
			return 0
		}
	})
	return extras
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
			resultID:          resultID,
			frame:             res.resultCallFrame.clone(),
			extraDigests:      collectSnapshotExtraDigestsLocked(c.eqClassExtraDigests, outputEqClasses),
			exportID:          nil,
			self:              res.self,
			objType:           res.objType,
			hasValue:          res.hasValue,
			persistedEnvelope: res.persistedEnvelope,
			outputEffectIDs:   slices.Clone(res.outputEffectIDs),
			row: persistdb.MirrorResult{
				ID:                   int64(resultID),
				SafeToPersistCache:   res.safeToPersistCache,
				DepOfPersistedResult: res.depOfPersistedResult,
				ExpiresAtUnix:        res.expiresAtUnix,
				CreatedAtUnixNano:    res.createdAtUnixNano,
				LastUsedAtUnixNano:   res.lastUsedAtUnixNano,
				SizeEstimateBytes:    res.sizeEstimateBytes,
				UsageIdentity:        res.usageIdentity,
				RecordType:           res.recordType,
				Description:          res.description,
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

	resultsByID := make(map[sharedResultID]*persistResultSnapshot, len(snapshot.results))
	for i := range snapshot.results {
		resultsByID[snapshot.results[i].resultID] = &snapshot.results[i]
	}

	for i := range snapshot.results {
		resultSnapshot := &snapshot.results[i]
		if resultSnapshot.frame == nil {
			return persistStateSnapshot{}, fmt.Errorf("persist result %d: missing result call frame", resultSnapshot.resultID)
		}

		exportID, err := persistedCallIDFromSnapshot(resultSnapshot.resultID, resultsByID, map[sharedResultID]struct{}{})
		if err != nil {
			return persistStateSnapshot{}, fmt.Errorf("persist result %d export ID: %w", resultSnapshot.resultID, err)
		}
		resultSnapshot.exportID = exportID

		env, err := c.persistResultEnvelope(ctx, resultSnapshot)
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
		outputEffectIDs, err := json.Marshal(resultSnapshot.outputEffectIDs)
		if err != nil {
			return persistStateSnapshot{}, fmt.Errorf("persist result %d output effect IDs: %w", resultSnapshot.resultID, err)
		}
		callFrameJSON, err := json.Marshal(resultSnapshot.frame)
		if err != nil {
			return persistStateSnapshot{}, fmt.Errorf("persist result %d call frame JSON: %w", resultSnapshot.resultID, err)
		}

		resultSnapshot.row.CallFrameJSON = string(callFrameJSON)
		resultSnapshot.row.SelfPayload = payload
		resultSnapshot.row.OutputEffectIDs = string(outputEffectIDs)
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

func persistedCallIDFromSnapshot(
	resultID sharedResultID,
	resultsByID map[sharedResultID]*persistResultSnapshot,
	visiting map[sharedResultID]struct{},
) (*call.ID, error) {
	snapshot := resultsByID[resultID]
	if snapshot == nil {
		return nil, fmt.Errorf("missing snapshot result")
	}
	if snapshot.exportID != nil {
		return snapshot.exportID, nil
	}
	if snapshot.frame == nil {
		return nil, fmt.Errorf("missing snapshot frame")
	}
	if _, seen := visiting[resultID]; seen {
		return nil, fmt.Errorf("cycle rebuilding export ID")
	}
	visiting[resultID] = struct{}{}
	defer delete(visiting, resultID)

	field := snapshot.frame.Field
	if snapshot.frame.Kind == ResultCallFrameKindSynthetic {
		field = snapshot.frame.SyntheticOp
	}
	if field == "" {
		return nil, fmt.Errorf("missing frame field")
	}

	var receiverID *call.ID
	if snapshot.frame.Receiver != nil {
		var err error
		receiverID, err = persistedCallIDFromSnapshot(sharedResultID(snapshot.frame.Receiver.ResultID), resultsByID, visiting)
		if err != nil {
			return nil, fmt.Errorf("receiver result %d: %w", snapshot.frame.Receiver.ResultID, err)
		}
	}

	var mod *call.Module
	if snapshot.frame.Module != nil {
		if snapshot.frame.Module.ResultRef == nil {
			return nil, fmt.Errorf("missing frame module result ref")
		}
		modID, err := persistedCallIDFromSnapshot(sharedResultID(snapshot.frame.Module.ResultRef.ResultID), resultsByID, visiting)
		if err != nil {
			return nil, fmt.Errorf("module result %d: %w", snapshot.frame.Module.ResultRef.ResultID, err)
		}
		mod = call.NewModule(modID, snapshot.frame.Module.Name, snapshot.frame.Module.Ref, snapshot.frame.Module.Pin)
	}

	args, err := persistedCallArgsFromSnapshot(snapshot.frame.Args, resultsByID, visiting)
	if err != nil {
		return nil, fmt.Errorf("args: %w", err)
	}
	implicitInputs, err := persistedCallArgsFromSnapshot(snapshot.frame.ImplicitInputs, resultsByID, visiting)
	if err != nil {
		return nil, fmt.Errorf("implicit inputs: %w", err)
	}

	rebuilt := receiverID
	rebuilt = rebuilt.Append(
		snapshot.frame.Type.toAST(),
		field,
		call.WithView(snapshot.frame.View),
		call.WithNth(int(snapshot.frame.Nth)),
		call.WithEffectIDs(snapshot.frame.EffectIDs),
		call.WithArgs(args...),
		call.WithImplicitInputs(implicitInputs...),
		call.WithModule(mod),
	)
	if rebuilt == nil {
		return nil, fmt.Errorf("rebuild returned nil")
	}
	for _, extra := range snapshot.extraDigests {
		rebuilt = rebuilt.With(call.WithExtraDigest(extra))
	}
	snapshot.exportID = rebuilt
	return rebuilt, nil
}

func persistedCallArgsFromSnapshot(
	frameArgs []*ResultCallFrameArg,
	resultsByID map[sharedResultID]*persistResultSnapshot,
	visiting map[sharedResultID]struct{},
) ([]*call.Argument, error) {
	if len(frameArgs) == 0 {
		return nil, nil
	}
	args := make([]*call.Argument, 0, len(frameArgs))
	for _, frameArg := range frameArgs {
		if frameArg == nil || frameArg.Value == nil {
			continue
		}
		lit, err := persistedCallLiteralFromSnapshot(frameArg.Value, resultsByID, visiting)
		if err != nil {
			return nil, fmt.Errorf("arg %q: %w", frameArg.Name, err)
		}
		args = append(args, call.NewArgument(frameArg.Name, lit, frameArg.IsSensitive))
	}
	return args, nil
}

func persistedCallLiteralFromSnapshot(
	frameLit *ResultCallFrameLiteral,
	resultsByID map[sharedResultID]*persistResultSnapshot,
	visiting map[sharedResultID]struct{},
) (call.Literal, error) {
	if frameLit == nil {
		return nil, fmt.Errorf("nil frame literal")
	}
	switch frameLit.Kind {
	case ResultCallFrameLiteralKindNull:
		return call.NewLiteralNull(), nil
	case ResultCallFrameLiteralKindBool:
		return call.NewLiteralBool(frameLit.BoolValue), nil
	case ResultCallFrameLiteralKindInt:
		return call.NewLiteralInt(frameLit.IntValue), nil
	case ResultCallFrameLiteralKindFloat:
		return call.NewLiteralFloat(frameLit.FloatValue), nil
	case ResultCallFrameLiteralKindString:
		return call.NewLiteralString(frameLit.StringValue), nil
	case ResultCallFrameLiteralKindEnum:
		return call.NewLiteralEnum(frameLit.EnumValue), nil
	case ResultCallFrameLiteralKindDigestedString:
		return call.NewLiteralDigestedString(frameLit.DigestedStringValue, frameLit.DigestedStringDigest), nil
	case ResultCallFrameLiteralKindResultRef:
		if frameLit.ResultRef == nil {
			return nil, fmt.Errorf("missing result ref")
		}
		id, err := persistedCallIDFromSnapshot(sharedResultID(frameLit.ResultRef.ResultID), resultsByID, visiting)
		if err != nil {
			return nil, fmt.Errorf("result ref %d: %w", frameLit.ResultRef.ResultID, err)
		}
		return call.NewLiteralID(id), nil
	case ResultCallFrameLiteralKindList:
		items := make([]call.Literal, 0, len(frameLit.ListItems))
		for i, item := range frameLit.ListItems {
			lit, err := persistedCallLiteralFromSnapshot(item, resultsByID, visiting)
			if err != nil {
				return nil, fmt.Errorf("list item %d: %w", i+1, err)
			}
			items = append(items, lit)
		}
		return call.NewLiteralList(items...), nil
	case ResultCallFrameLiteralKindObject:
		fields := make([]*call.Argument, 0, len(frameLit.ObjectFields))
		for _, field := range frameLit.ObjectFields {
			if field == nil || field.Value == nil {
				continue
			}
			lit, err := persistedCallLiteralFromSnapshot(field.Value, resultsByID, visiting)
			if err != nil {
				return nil, fmt.Errorf("object field %q: %w", field.Name, err)
			}
			fields = append(fields, call.NewArgument(field.Name, lit, field.IsSensitive))
		}
		return call.NewLiteralObject(fields...), nil
	default:
		return nil, fmt.Errorf("unsupported frame literal kind %q", frameLit.Kind)
	}
}

func (c *cache) persistResultEnvelope(ctx context.Context, snapshot *persistResultSnapshot) (PersistedResultEnvelope, error) {
	if snapshot != nil && snapshot.persistedEnvelope != nil {
		return *snapshot.persistedEnvelope, nil
	}
	if snapshot == nil || !snapshot.hasValue {
		return PersistedResultEnvelope{
			Version: 1,
			Kind:    persistedResultKindNull,
		}, nil
	}
	if snapshot.exportID == nil {
		return PersistedResultEnvelope{}, fmt.Errorf("result has no reconstructable frame ID and no persisted envelope: missing export ID")
	}
	shared := &sharedResult{
		self:     snapshot.self,
		objType:  snapshot.objType,
		hasValue: snapshot.hasValue,
	}
	typedRes := Result[Typed]{
		shared: shared,
		id:     snapshot.exportID,
	}
	var anyRes AnyResult = typedRes
	if snapshot.objType != nil {
		objRes, err := snapshot.objType.New(typedRes)
		if err != nil {
			return PersistedResultEnvelope{}, fmt.Errorf("reconstruct object result: %w", err)
		}
		anyRes = objRes
	}
	persistCtx := context.WithoutCancel(ctx)
	persistCtx = ContextWithID(persistCtx, snapshot.exportID)
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
