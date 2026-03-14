package dagql

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
)

const (
	debugEGraphTrace       = true
	egraphTraceFormatV1    = 1
	egraphTraceMessageName = "dagql_egraph_trace"
)

type EGraphDebugSnapshot struct {
	TraceFormatVersion int                        `json:"trace_format_version"`
	BootID             string                     `json:"boot_id"`
	CapturedAtSeq      uint64                     `json:"captured_at_seq"`
	CapturedAtTime     string                     `json:"captured_at_time"`
	Results            []EGraphDebugResult        `json:"results"`
	Terms              []EGraphDebugTerm          `json:"terms"`
	ResultTerms        []EGraphDebugResultTerm    `json:"result_terms"`
	Digests            []EGraphDebugDigestMapping `json:"digests"`
	EqClasses          []EGraphDebugEqClass       `json:"eq_classes"`
}

type EGraphDebugResult struct {
	SharedResultID        uint64                     `json:"shared_result_id"`
	OutputEqClassIDs      []uint64                   `json:"output_eq_class_ids,omitempty"`
	OutputEffectIDs       []string                   `json:"output_effect_ids,omitempty"`
	RecordType            string                     `json:"record_type,omitempty"`
	Description           string                     `json:"description,omitempty"`
	TypeName              string                     `json:"type_name,omitempty"`
	RefCount              int64                      `json:"ref_count"`
	HasValue              bool                       `json:"has_value"`
	PayloadState          string                     `json:"payload_state"`
	DepOfPersistedResult  bool                       `json:"dep_of_persisted_result"`
	ExplicitDeps          []uint64                   `json:"explicit_dep_ids,omitempty"`
	HeldDependencyResults int                        `json:"held_dependency_results_count"`
	SnapshotLinks         []PersistedSnapshotRefLink `json:"snapshot_links,omitempty"`
}

type EGraphDebugTerm struct {
	TermID     uint64   `json:"term_id"`
	SelfDigest string   `json:"self_digest"`
	InputEqIDs []uint64 `json:"input_eq_ids"`
	TermDigest string   `json:"term_digest"`
	OutputEqID uint64   `json:"output_eq_id"`
}

type EGraphDebugInputProvenance struct {
	Kind string `json:"kind"`
}

type EGraphDebugResultTerm struct {
	SharedResultID  uint64                       `json:"shared_result_id"`
	TermID          uint64                       `json:"term_id"`
	InputProvenance []EGraphDebugInputProvenance `json:"input_provenance,omitempty"`
}

type EGraphDebugDigestMapping struct {
	Digest    string `json:"digest"`
	EqClassID uint64 `json:"eq_class_id"`
}

type EGraphDebugEqClass struct {
	EqClassID uint64   `json:"eq_class_id"`
	Digests   []string `json:"digests"`
}

func newTraceBootID() string {
	return fmt.Sprintf("boot-%d", time.Now().UnixNano())
}

func debugInputProvenance(inputs []egraphInputProvenanceKind) []map[string]any {
	out := make([]map[string]any, 0, len(inputs))
	for _, input := range inputs {
		out = append(out, map[string]any{
			"kind": string(input),
		})
	}
	return out
}

func (c *cache) nextTraceSeq() uint64 {
	return atomic.AddUint64(&c.traceSeq, 1)
}

func (c *cache) nextPersistBatchID() string {
	return fmt.Sprintf("persist-batch-%d", atomic.AddUint64(&c.tracePersistBatch, 1))
}

func (c *cache) nextImportRunID() string {
	return fmt.Sprintf("import-run-%d", atomic.AddUint64(&c.traceImportRuns, 1))
}

func (c *cache) traceEnabled() bool {
	return debugEGraphTrace && c != nil
}

func (c *cache) trace(ctx context.Context, event string, args ...any) {
	if !c.traceEnabled() {
		return
	}
	base := []any{
		"trace_format_version", egraphTraceFormatV1,
		"boot_id", c.traceBootID,
		"seq", c.nextTraceSeq(),
		"event", event,
	}
	if md, err := engine.ClientMetadataFromContext(ctx); err == nil {
		base = append(base,
			"client_id", md.ClientID,
			"session_id", md.SessionID,
		)
	}
	if id := CurrentID(ctx); id != nil {
		base = append(base, "call_digest", id.Digest().String())
	}
	base = append(base, args...)
	slog.InfoContext(ctx, egraphTraceMessageName, base...)
}

func (c *cache) traceLazy(ctx context.Context, event string, build func() []any) {
	if !c.traceEnabled() {
		return
	}
	c.trace(ctx, event, build()...)
}

func (c *cache) tracePersistStoreWipedSchemaMismatch(ctx context.Context, expected, actual string) {
	c.traceLazy(ctx, "persist_store_wiped_schema_mismatch", func() []any {
		return []any{"phase", "startup", "expected_schema_version", expected, "actual_schema_version", actual}
	})
}

func (c *cache) tracePersistStoreWipedUncleanShutdown(ctx context.Context, cleanShutdown string) {
	c.traceLazy(ctx, "persist_store_wiped_unclean_shutdown", func() []any {
		return []any{"phase", "startup", "clean_shutdown", cleanShutdown}
	})
}

func (c *cache) tracePersistStoreWipedImportFailure(ctx context.Context, err error) {
	c.traceLazy(ctx, "persist_store_wiped_import_failure", func() []any {
		return []any{"phase", "startup", "error", err.Error()}
	})
}

func (c *cache) traceRefReleased(ctx context.Context, res *sharedResult, refCount int64) {
	c.traceLazy(ctx, "ref_released", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id, "ref_count", refCount}
	})
}

func (c *cache) traceRefAcquired(ctx context.Context, res *sharedResult, refCount int64) {
	c.traceLazy(ctx, "ref_acquired", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id, "ref_count", refCount}
	})
}

func (c *cache) traceResultDigestSeeded(ctx context.Context, requestDigest string, outputDigest string, extras []call.ExtraDigest) {
	c.traceLazy(ctx, "result_digest_seeded", func() []any {
		out := make([]string, 0, len(extras))
		for _, extra := range extras {
			if extra.Digest != "" {
				out = append(out, extra.Digest.String())
			}
		}
		return []any{"phase", "runtime", "id_digest", requestDigest, "output_digest", outputDigest, "output_extra_digests", out}
	})
}

func (c *cache) traceExplicitDepAdded(ctx context.Context, resID, depID sharedResultID, reason string) {
	c.traceLazy(ctx, "explicit_dep_added", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "dep_shared_result_id", depID, "reason", reason}
	})
}

func (c *cache) traceExplicitDepRemoved(ctx context.Context, resID, depID sharedResultID, phase string) {
	c.traceLazy(ctx, "explicit_dep_removed", func() []any {
		return []any{"phase", phase, "shared_result_id", resID, "dep_shared_result_id", depID}
	})
}

func (c *cache) traceEqClassCreated(ctx context.Context, classID eqClassID, dig string) {
	c.traceLazy(ctx, "eq_class_created", func() []any {
		return []any{"phase", "runtime", "eq_class_id", classID, "digest", dig}
	})
}

func (c *cache) traceEqClassMerged(ctx context.Context, ids []eqClassID, root eqClassID) {
	c.traceLazy(ctx, "eq_class_merged", func() []any {
		return []any{"phase", "runtime", "merge_ids", ids, "root_eq_class_id", root}
	})
}

func (c *cache) traceTermInputsRepaired(ctx context.Context, termID egraphTermID, oldInputs, newInputs []eqClassID) {
	c.traceLazy(ctx, "term_inputs_repaired", func() []any {
		return []any{"phase", "runtime", "term_id", termID, "old_input_eq_ids", oldInputs, "new_input_eq_ids", newInputs}
	})
}

func (c *cache) traceTermRehomedUnderEqClasses(ctx context.Context, termID egraphTermID, inputEqIDs []eqClassID) {
	c.traceLazy(ctx, "term_rehomed_under_eq_classes", func() []any {
		return []any{"phase", "runtime", "term_id", termID, "input_eq_ids", inputEqIDs}
	})
}

func (c *cache) traceTermDigestRecomputed(ctx context.Context, termID egraphTermID, oldTermDigest, newTermDigest string) {
	c.traceLazy(ctx, "term_digest_recomputed", func() []any {
		return []any{"phase", "runtime", "term_id", termID, "old_term_digest", oldTermDigest, "new_term_digest", newTermDigest}
	})
}

func (c *cache) traceTermOutputsMerged(ctx context.Context, termID, otherTermID egraphTermID, lhs, rhs eqClassID) {
	c.traceLazy(ctx, "term_outputs_merged", func() []any {
		return []any{"phase", "runtime", "term_id", termID, "other_term_id", otherTermID, "lhs_output_eq_id", lhs, "rhs_output_eq_id", rhs}
	})
}

func (c *cache) traceLookupAttempt(ctx context.Context, requestDigest, selfDigest string, inputDigests []digest.Digest, persistable bool) {
	c.traceLazy(ctx, "lookup_attempt", func() []any {
		return []any{"phase", "runtime", "request_digest", requestDigest, "self_digest", selfDigest, "input_digests", inputDigests, "persistable", persistable}
	})
}

func (c *cache) traceLookupMissNoMatch(ctx context.Context, requestDigest string, primaryLookupPossible bool, missingInputIndex int, termDigest string, termSetSize int) {
	c.traceLazy(ctx, "lookup_miss_reason", func() []any {
		return []any{"phase", "runtime", "request_digest", requestDigest, "reason", "no_matching_result", "primary_lookup_possible", primaryLookupPossible, "missing_input_index", missingInputIndex, "term_digest", termDigest, "term_set_size", termSetSize}
	})
}

func (c *cache) traceLookupMissUndecodableEnvelope(ctx context.Context, requestDigest string, resultID sharedResultID) {
	c.traceLazy(ctx, "lookup_miss_reason", func() []any {
		return []any{"phase", "runtime", "request_digest", requestDigest, "reason", "persisted_envelope_not_decodable_in_context", "shared_result_id", resultID}
	})
}

func (c *cache) traceLookupHit(ctx context.Context, requestDigest string, res *sharedResult, hitTerm *egraphTerm, termDigest string) {
	c.traceLazy(ctx, "lookup_hit", func() []any {
		termID := egraphTermID(0)
		if hitTerm != nil {
			termID = hitTerm.id
		}
		return []any{"phase", "runtime", "request_digest", requestDigest, "shared_result_id", res.id, "term_id", termID, "term_digest", termDigest}
	})
}

func (c *cache) traceResultCreated(ctx context.Context, res *sharedResult) {
	c.traceLazy(ctx, "result_created", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id, "record_type", res.recordType, "description", res.description}
	})
}

func (c *cache) tracePersistKeyIndexAdded(ctx context.Context, resID sharedResultID, resultKey string, phase string, importRunID string) {
	c.traceLazy(ctx, "persist_key_index_added", func() []any {
		args := []any{"phase", phase, "shared_result_id", resID, "result_key", resultKey}
		if importRunID != "" {
			args = append(args, "import_run_id", importRunID)
		}
		return args
	})
}

func (c *cache) tracePersistKeyIndexRemoved(ctx context.Context, resID sharedResultID, resultKey string) {
	c.traceLazy(ctx, "persist_key_index_removed", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "result_key", resultKey}
	})
}

func (c *cache) traceResultTermAssocUpdated(ctx context.Context, resID sharedResultID, termID egraphTermID, inputProvenance []egraphInputProvenanceKind) {
	c.traceLazy(ctx, "result_term_assoc_updated", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "term_id", termID, "input_provenance", debugInputProvenance(inputProvenance)}
	})
}

func (c *cache) traceResultTermAssocAdded(ctx context.Context, resID sharedResultID, termID egraphTermID, inputProvenance []egraphInputProvenanceKind) {
	c.traceLazy(ctx, "result_term_assoc_added", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "term_id", termID, "input_provenance", debugInputProvenance(inputProvenance)}
	})
}

func (c *cache) traceResultTermAssocRemoved(ctx context.Context, resID sharedResultID, termID egraphTermID) {
	c.traceLazy(ctx, "result_term_assoc_removed", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "term_id", termID}
	})
}

func (c *cache) traceTermCreated(ctx context.Context, phase string, importRunID string, term *egraphTerm) {
	c.traceLazy(ctx, "term_created", func() []any {
		args := []any{"phase", phase, "term_id", term.id, "self_digest", term.selfDigest.String(), "term_digest", term.termDigest, "output_eq_id", term.outputEqID}
		if importRunID != "" {
			args = append(args, "import_run_id", importRunID)
		}
		return args
	})
}

func (c *cache) traceTermRemoved(ctx context.Context, termID egraphTermID) {
	c.traceLazy(ctx, "term_removed", func() []any {
		return []any{"phase", "runtime", "term_id", termID}
	})
}

func (c *cache) tracePersistRootMarked(ctx context.Context, resID sharedResultID, rootID sharedResultID, phase, importRunID string) {
	c.traceLazy(ctx, "persist_root_marked", func() []any {
		args := []any{"phase", phase, "shared_result_id", resID, "root_shared_result_id", rootID}
		if importRunID != "" {
			args = append(args, "import_run_id", importRunID)
		}
		return args
	})
}

func (c *cache) traceResultRemoved(ctx context.Context, res *sharedResult) {
	c.traceLazy(ctx, "result_removed", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id}
	})
}

func (c *cache) tracePersistBatchAppliedSkip(ctx context.Context, batchID, resultKey, reason string) {
	c.traceLazy(ctx, "persist_batch_applied", func() []any {
		return []any{"phase", "persist-apply", "batch_id", batchID, "result_key", resultKey, "reason", reason}
	})
}

func (c *cache) tracePersistRootMemberRemoved(ctx context.Context, batchID, rootResultKey, resultKey string) {
	c.traceLazy(ctx, "persist_root_member_removed", func() []any {
		return []any{"phase", "persist-apply", "batch_id", batchID, "root_result_key", rootResultKey, "result_key", resultKey}
	})
}

func (c *cache) tracePersistRootDeleted(ctx context.Context, batchID, resultKey string) {
	c.traceLazy(ctx, "persist_root_deleted", func() []any {
		return []any{"phase", "persist-apply", "batch_id", batchID, "result_key", resultKey}
	})
}

func (c *cache) tracePersistOrphanGCDeleted(ctx context.Context, batchID, resultKey string) {
	c.traceLazy(ctx, "persist_orphan_gc_deleted", func() []any {
		return []any{"phase", "persist-apply", "batch_id", batchID, "result_key", resultKey}
	})
}

func (c *cache) tracePersistBatchApplied(ctx context.Context, batchID, resultKey string) {
	c.traceLazy(ctx, "persist_batch_applied", func() []any {
		return []any{"phase", "persist-apply", "batch_id", batchID, "result_key", resultKey}
	})
}

func (c *cache) tracePersistRootAdded(ctx context.Context, batchID, resultKey, phase string, importRunID string) {
	c.traceLazy(ctx, "persist_root_added", func() []any {
		args := []any{"phase", phase, "result_key", resultKey}
		if batchID != "" {
			args = append(args, "batch_id", batchID)
		}
		if importRunID != "" {
			args = append(args, "import_run_id", importRunID)
		}
		return args
	})
}

func (c *cache) tracePersistRootMemberAdded(ctx context.Context, batchID, rootResultKey, resultKey, phase, importRunID string, resID sharedResultID) {
	c.traceLazy(ctx, "persist_root_member_added", func() []any {
		args := []any{"phase", phase, "root_result_key", rootResultKey, "result_key", resultKey}
		if resID != 0 {
			args = append(args, "shared_result_id", resID)
		}
		if batchID != "" {
			args = append(args, "batch_id", batchID)
		}
		if importRunID != "" {
			args = append(args, "import_run_id", importRunID)
		}
		return args
	})
}

func (c *cache) tracePersistBatchBuildFailed(ctx context.Context, resultKey string, err error) {
	c.traceLazy(ctx, "persist_batch_build_failed", func() []any {
		return []any{"phase", "persist-export", "result_key", resultKey, "error", err.Error()}
	})
}

func (c *cache) tracePersistBatchBuilt(ctx context.Context, batchID, resultKey string, deleteRoot bool) {
	c.traceLazy(ctx, "persist_batch_built", func() []any {
		return []any{"phase", "persist-export", "batch_id", batchID, "result_key", resultKey, "delete_root", deleteRoot}
	})
}

func (c *cache) tracePersistWatchCleared(ctx context.Context, rootID, memberID sharedResultID) {
	c.traceLazy(ctx, "persist_watch_cleared", func() []any {
		return []any{"phase", "persist-export", "shared_result_id", rootID, "watched_member_shared_result_id", memberID}
	})
}

func (c *cache) tracePersistRootDeferred(ctx context.Context, root *sharedResult, memberCount int) {
	c.traceLazy(ctx, "persist_root_deferred", func() []any {
		return []any{"phase", "persist-export", "shared_result_id", root.id, "member_count", memberCount}
	})
}

func (c *cache) tracePersistMemberNotReady(ctx context.Context, rootID, memberID sharedResultID) {
	c.traceLazy(ctx, "persist_member_not_ready", func() []any {
		return []any{"phase", "persist-export", "shared_result_id", memberID, "root_shared_result_id", rootID}
	})
}

func (c *cache) traceLazyRealized(ctx context.Context, memberID, rootID sharedResultID) {
	c.traceLazy(ctx, "lazy_realized", func() []any {
		return []any{"phase", "runtime", "shared_result_id", memberID, "root_shared_result_id", rootID}
	})
}

func (c *cache) tracePersistRetryTriggered(ctx context.Context, resultID sharedResultID) {
	c.traceLazy(ctx, "persist_root_retry_triggered", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resultID}
	})
}

func (c *cache) tracePersistRetryRegistered(ctx context.Context, resultID sharedResultID) {
	c.traceLazy(ctx, "persist_retry_registered", func() []any {
		return []any{"phase", "persist-export", "shared_result_id", resultID}
	})
}

func (c *cache) tracePersistRetryNoop(ctx context.Context, rootID sharedResultID) {
	c.traceLazy(ctx, "persist_retry_noop", func() []any {
		return []any{"phase", "runtime", "shared_result_id", rootID}
	})
}

func (c *cache) tracePersistRetrySucceeded(ctx context.Context, resultID sharedResultID) {
	c.traceLazy(ctx, "persist_retry_succeeded", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resultID}
	})
}

func (c *cache) tracePersistedPayloadImportedEager(ctx context.Context, importRunID string, resID sharedResultID, resultKey, payloadState string) {
	c.traceLazy(ctx, "persisted_payload_imported_eager", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", resID, "result_key", resultKey, "payload_state", payloadState}
	})
}

func (c *cache) tracePersistedPayloadImportedLazy(ctx context.Context, importRunID string, resID sharedResultID, resultKey, payloadKind, typeName string) {
	c.traceLazy(ctx, "persisted_payload_imported_lazy", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", resID, "result_key", resultKey, "payload_kind", payloadKind, "type_name", typeName}
	})
}

func (c *cache) traceImportResultLoaded(ctx context.Context, importRunID string, resID sharedResultID, callFrameJSON string) {
	c.traceLazy(ctx, "import_result_loaded", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", resID, "call_frame_json", callFrameJSON}
	})
}

func (c *cache) traceImportResultSnapshotLinkLoaded(ctx context.Context, importRunID string, resID sharedResultID, refKey, role, slot string) {
	c.traceLazy(ctx, "import_result_snapshot_link_loaded", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", resID, "ref_key", refKey, "role", role, "slot", slot}
	})
}

func (c *cache) traceImportResultTermLoaded(ctx context.Context, importRunID string, resID sharedResultID, resultKey string, termID egraphTermID, selfDigest string, inputProvenance []egraphInputProvenanceKind) {
	c.traceLazy(ctx, "import_result_term_loaded", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", resID, "result_key", resultKey, "term_id", termID, "self_digest", selfDigest, "input_provenance", debugInputProvenance(inputProvenance)}
	})
}

func (c *cache) traceImportResultDepLoaded(ctx context.Context, importRunID string, parentID sharedResultID, depID sharedResultID) {
	c.traceLazy(ctx, "import_result_dep_loaded", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", parentID, "dep_shared_result_id", depID}
	})
}

func (c *cache) tracePersistedPayloadDecodeFailed(ctx context.Context, res *sharedResult, env *PersistedResultEnvelope, err error) {
	c.traceLazy(ctx, "persisted_payload_decode_failed", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id, "payload_kind", env.Kind, "type_name", env.TypeName, "error", err.Error()}
	})
}

func (c *cache) tracePersistedPayloadDecoded(ctx context.Context, res *sharedResult, env *PersistedResultEnvelope) {
	c.traceLazy(ctx, "persisted_payload_decoded", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id, "payload_kind", env.Kind, "type_name", env.TypeName}
	})
}

func (c *cache) DebugEGraphSnapshot() *EGraphDebugSnapshot {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()

	snap := &EGraphDebugSnapshot{
		TraceFormatVersion: egraphTraceFormatV1,
		BootID:             c.traceBootID,
		CapturedAtSeq:      atomic.LoadUint64(&c.traceSeq),
		CapturedAtTime:     time.Now().UTC().Format(time.RFC3339Nano),
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
		depIDs := make([]uint64, 0, len(res.deps))
		for depID := range res.deps {
			depIDs = append(depIDs, uint64(depID))
		}
		slices.Sort(depIDs)
		outputEqIDs := make([]uint64, 0, len(c.resultOutputEqClasses[resultID]))
		for outputEqID := range c.outputEqClassesForResultLocked(resultID) {
			outputEqIDs = append(outputEqIDs, uint64(outputEqID))
		}
		slices.Sort(outputEqIDs)
		links := append([]PersistedSnapshotRefLink(nil), res.persistedSnapshotLinks...)
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

		typeName := ""
		if res.self != nil && res.self.Type() != nil {
			typeName = res.self.Type().Name()
		} else if res.objType != nil {
			typeName = res.objType.Typed().Type().Name()
		}

		payloadState := "uninitialized"
		switch {
		case res.persistedEnvelope != nil && !res.hasValue:
			payloadState = "imported_lazy_envelope"
		case res.hasValue && res.self == nil:
			payloadState = "nil"
		case res.hasValue:
			payloadState = "materialized"
		}

		snap.Results = append(snap.Results, EGraphDebugResult{
			SharedResultID:        uint64(res.id),
			OutputEqClassIDs:      outputEqIDs,
			OutputEffectIDs:       append([]string(nil), res.outputEffectIDs...),
			RecordType:            res.recordType,
			Description:           res.description,
			TypeName:              typeName,
			RefCount:              atomic.LoadInt64(&res.refCount),
			HasValue:              res.hasValue,
			PayloadState:          payloadState,
			DepOfPersistedResult:  res.depOfPersistedResult,
			ExplicitDeps:          depIDs,
			HeldDependencyResults: len(res.heldDependencyResults),
			SnapshotLinks:         links,
		})
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
		inputEqIDs := make([]uint64, 0, len(term.inputEqIDs))
		for _, in := range term.inputEqIDs {
			inputEqIDs = append(inputEqIDs, uint64(in))
		}
		snap.Terms = append(snap.Terms, EGraphDebugTerm{
			TermID:     uint64(term.id),
			SelfDigest: term.selfDigest.String(),
			InputEqIDs: inputEqIDs,
			TermDigest: term.termDigest,
			OutputEqID: uint64(term.outputEqID),
		})
	}

	for _, resultID := range resultIDs {
		assocTermIDs := make([]egraphTermID, 0, len(c.termIDsForResultLocked(resultID)))
		for termID := range c.termIDsForResultLocked(resultID) {
			assocTermIDs = append(assocTermIDs, termID)
		}
		if len(assocTermIDs) == 0 {
			continue
		}
		slices.Sort(assocTermIDs)
		for _, termID := range assocTermIDs {
			inputProvenance := make([]EGraphDebugInputProvenance, 0, len(c.termInputProvenance[termID]))
			for _, input := range c.termInputProvenance[termID] {
				inputProvenance = append(inputProvenance, EGraphDebugInputProvenance{
					Kind: string(input),
				})
			}
			snap.ResultTerms = append(snap.ResultTerms, EGraphDebugResultTerm{
				SharedResultID:  uint64(resultID),
				TermID:          uint64(termID),
				InputProvenance: inputProvenance,
			})
		}
	}

	digests := make([]string, 0, len(c.egraphDigestToClass))
	for dig := range c.egraphDigestToClass {
		digests = append(digests, dig)
	}
	slices.Sort(digests)
	classMembers := make(map[eqClassID][]string)
	for _, dig := range digests {
		root := c.findEqClassLocked(c.egraphDigestToClass[dig])
		snap.Digests = append(snap.Digests, EGraphDebugDigestMapping{
			Digest:    dig,
			EqClassID: uint64(root),
		})
		classMembers[root] = append(classMembers[root], dig)
	}
	classIDs := make([]eqClassID, 0, len(classMembers))
	for classID := range classMembers {
		classIDs = append(classIDs, classID)
	}
	slices.Sort(classIDs)
	for _, classID := range classIDs {
		members := append([]string(nil), classMembers[classID]...)
		slices.Sort(members)
		snap.EqClasses = append(snap.EqClasses, EGraphDebugEqClass{
			EqClassID: uint64(classID),
			Digests:   members,
		})
	}

	return snap
}
