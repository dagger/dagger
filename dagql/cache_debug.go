package dagql

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
	"slices"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
)

const (
	debugEGraphTrace       = false
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

type CacheDebugSnapshot struct {
	TraceFormatVersion      int                           `json:"trace_format_version"`
	BootID                  string                        `json:"boot_id"`
	CapturedAtSeq           uint64                        `json:"captured_at_seq"`
	CapturedAtTime          string                        `json:"captured_at_time"`
	Results                 []CacheDebugResult            `json:"results"`
	ResultDigestIndexes     []CacheDebugResultDigestIndex `json:"result_digest_indexes"`
	Terms                   []EGraphDebugTerm             `json:"terms"`
	ResultTerms             []EGraphDebugResultTerm       `json:"result_terms"`
	Digests                 []EGraphDebugDigestMapping    `json:"digests"`
	EqClasses               []EGraphDebugEqClass          `json:"eq_classes"`
	OngoingCalls            []CacheDebugOngoingCall       `json:"ongoing_calls,omitempty"`
	OngoingArbitraryCalls   []CacheDebugArbitraryCall     `json:"ongoing_arbitrary_calls,omitempty"`
	CompletedArbitraryCalls []CacheDebugArbitraryCall     `json:"completed_arbitrary_calls,omitempty"`
}

type EGraphDebugResult struct {
	SharedResultID         uint64                     `json:"shared_result_id"`
	OutputEqClassIDs       []uint64                   `json:"output_eq_class_ids,omitempty"`
	OutputEffectIDs        []string                   `json:"output_effect_ids,omitempty"`
	RecordType             string                     `json:"record_type,omitempty"`
	Description            string                     `json:"description,omitempty"`
	TypeName               string                     `json:"type_name,omitempty"`
	IncomingOwnershipCount int64                      `json:"incoming_ownership_count"`
	HasValue               bool                       `json:"has_value"`
	PayloadState           string                     `json:"payload_state"`
	HasPersistedEdge       bool                       `json:"has_persisted_edge"`
	ExplicitDeps           []uint64                   `json:"explicit_dep_ids,omitempty"`
	HeldDependencyResults  int                        `json:"held_dependency_results_count"`
	SnapshotLinks          []PersistedSnapshotRefLink `json:"snapshot_links,omitempty"`
}

type CacheDebugResult struct {
	EGraphDebugResult
	ResultCall                            *ResultCall `json:"result_call,omitempty"`
	ResultCallRecipeDigest                string      `json:"result_call_recipe_digest,omitempty"`
	ResultCallRecipeDigestError           string      `json:"result_call_recipe_digest_error,omitempty"`
	ResultCallContentPreferredDigest      string      `json:"result_call_content_preferred_digest,omitempty"`
	ResultCallContentPreferredDigestError string      `json:"result_call_content_preferred_digest_error,omitempty"`
	ResultCallInputDigests                []string    `json:"result_call_input_digests,omitempty"`
	ResultCallInputDigestsError           string      `json:"result_call_input_digests_error,omitempty"`
	AssociatedTermIDs                     []uint64    `json:"associated_term_ids,omitempty"`
	IndexedDigests                        []string    `json:"indexed_digests,omitempty"`
	SafeToPersistCache                    bool        `json:"safe_to_persist_cache"`
	ExpiresAtUnix                         int64       `json:"expires_at_unix,omitempty"`
	CreatedAtUnixNano                     int64       `json:"created_at_unix_nano,omitempty"`
	LastUsedAtUnixNano                    int64       `json:"last_used_at_unix_nano,omitempty"`
	SizeEstimateBytes                     int64       `json:"size_estimate_bytes"`
	UsageIdentity                         string      `json:"usage_identity,omitempty"`
	PersistedEnvelopeKind                 string      `json:"persisted_envelope_kind,omitempty"`
	PersistedEnvelopeTypeName             string      `json:"persisted_envelope_type_name,omitempty"`
}

type CacheDebugResultDigestIndex struct {
	Digest          string   `json:"digest"`
	SharedResultIDs []uint64 `json:"shared_result_ids"`
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

type CacheDebugOngoingCall struct {
	CallKey           string `json:"call_key"`
	ConcurrencyKey    string `json:"concurrency_key,omitempty"`
	Waiters           int    `json:"waiters"`
	IsPersistable     bool   `json:"is_persistable"`
	TTLSeconds        int64  `json:"ttl_seconds,omitempty"`
	Completed         bool   `json:"completed"`
	Error             string `json:"error,omitempty"`
	SharedResultID    uint64 `json:"shared_result_id,omitempty"`
	ResultDescription string `json:"result_description,omitempty"`
	ResultRecordType  string `json:"result_record_type,omitempty"`
	ResultTypeName    string `json:"result_type_name,omitempty"`
}

type CacheDebugArbitraryCall struct {
	CallKey           string `json:"call_key"`
	Waiters           int    `json:"waiters"`
	OwnerSessionCount int    `json:"owner_session_count"`
	Completed         bool   `json:"completed"`
	HasValue          bool   `json:"has_value"`
	ValueType         string `json:"value_type,omitempty"`
	Error             string `json:"error,omitempty"`
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

func (c *Cache) nextTraceSeq() uint64 {
	return atomic.AddUint64(&c.traceSeq, 1)
}

func (c *Cache) nextPersistBatchID() string {
	return fmt.Sprintf("persist-batch-%d", atomic.AddUint64(&c.tracePersistBatch, 1))
}

func (c *Cache) nextImportRunID() string {
	return fmt.Sprintf("import-run-%d", atomic.AddUint64(&c.traceImportRuns, 1))
}

func (c *Cache) traceEnabled() bool {
	return debugEGraphTrace && c != nil
}

func (c *Cache) trace(ctx context.Context, event string, args ...any) {
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
	if call := CurrentCall(ctx); call != nil {
		base = append(base, "call_kind", call.Kind)
		if field, err := resultCallIdentityField(call); err == nil {
			base = append(base, "call_field", field)
		}
	}
	base = append(base, args...)
	slog.InfoContext(ctx, egraphTraceMessageName, base...)
}

func (c *Cache) traceLazy(ctx context.Context, event string, build func() []any) {
	if !c.traceEnabled() {
		return
	}
	c.trace(ctx, event, build()...)
}

func (c *Cache) tracePersistStoreWipedSchemaMismatch(ctx context.Context, expected, actual string) {
	c.traceLazy(ctx, "persist_store_wiped_schema_mismatch", func() []any {
		return []any{"phase", "startup", "expected_schema_version", expected, "actual_schema_version", actual}
	})
}

func (c *Cache) tracePersistStoreWipedUncleanShutdown(ctx context.Context, cleanShutdown string) {
	c.traceLazy(ctx, "persist_store_wiped_unclean_shutdown", func() []any {
		return []any{"phase", "startup", "clean_shutdown", cleanShutdown}
	})
}

func (c *Cache) tracePersistStoreWipedImportFailure(ctx context.Context, err error) {
	c.traceLazy(ctx, "persist_store_wiped_import_failure", func() []any {
		return []any{"phase", "startup", "error", err.Error()}
	})
}

func (c *Cache) traceRefReleased(ctx context.Context, res *sharedResult, ownershipCount int64) {
	c.traceLazy(ctx, "ref_released", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id, "incoming_ownership_count", ownershipCount}
	})
}

func (c *Cache) traceRefUnderflow(ctx context.Context, res *sharedResult, ownershipCount int64) {
	c.traceLazy(ctx, "ref_underflow", func() []any {
		state := res.loadPayloadState()
		payloadState := "uninitialized"
		switch {
		case res == nil:
			payloadState = "unknown"
		case state.persistedEnvelope != nil && !state.hasValue:
			payloadState = "imported_lazy_envelope"
		case state.hasValue && state.self == nil:
			payloadState = "nil"
		case state.hasValue:
			payloadState = "materialized"
		}
		args := []any{
			"phase", "runtime",
			"shared_result_id", res.id,
			"incoming_ownership_count", ownershipCount,
			"record_type", res.recordType,
			"description", res.description,
			"payload_state", payloadState,
		}
		if state.self != nil {
			args = append(args, "type_name", fmt.Sprintf("%T", state.self))
		}
		args = append(args, "stack", string(debug.Stack()))
		return args
	})
}

func (c *Cache) traceRefAcquired(ctx context.Context, res *sharedResult, ownershipCount int64) {
	c.traceLazy(ctx, "ref_acquired", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id, "incoming_ownership_count", ownershipCount}
	})
}

func (c *Cache) traceSessionResultTracked(ctx context.Context, sessionID string, res AnyResult, hitCache bool, trackedCount, trackedCountForResult int) {
	c.traceLazy(ctx, "session_result_tracked", func() []any {
		shared := res.cacheSharedResult()
		args := []any{
			"phase", "runtime",
			"session_id", sessionID,
			"hit_cache", hitCache,
			"tracked_count", trackedCount,
			"tracked_count_for_result", trackedCountForResult,
		}
		if shared != nil {
			args = append(args,
				"shared_result_id", shared.id,
				"incoming_ownership_count", shared.incomingOwnershipCount,
				"record_type", shared.recordType,
				"description", shared.description,
			)
			if frame := shared.loadResultCall(); frame != nil {
				if field, err := resultCallIdentityField(frame); err == nil {
					args = append(args, "result_call_field", field)
				}
			}
		}
		return args
	})
}

func (c *Cache) traceSessionResultReleasing(ctx context.Context, sessionID string, res AnyResult, reason string, releaseIndex, totalTracked, trackedCountForResult, releaseOrdinalForResult int) {
	c.traceLazy(ctx, "session_result_releasing", func() []any {
		shared := res.cacheSharedResult()
		args := []any{
			"phase", "runtime",
			"reason", reason,
			"session_id", sessionID,
			"release_index", releaseIndex,
			"total_tracked", totalTracked,
			"tracked_count_for_result", trackedCountForResult,
			"release_ordinal_for_result", releaseOrdinalForResult,
		}
		if shared != nil {
			args = append(args,
				"shared_result_id", shared.id,
				"incoming_ownership_count", shared.incomingOwnershipCount,
				"record_type", shared.recordType,
				"description", shared.description,
			)
			if frame := shared.loadResultCall(); frame != nil {
				if field, err := resultCallIdentityField(frame); err == nil {
					args = append(args, "result_call_field", field)
				}
			}
		}
		return args
	})
}

func (c *Cache) traceResultDigestSeeded(ctx context.Context, requestDigest string, outputDigest string, extras []call.ExtraDigest) {
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

func (c *Cache) traceExplicitDepAdded(ctx context.Context, resID, depID sharedResultID, reason string) {
	c.traceLazy(ctx, "explicit_dep_added", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "dep_shared_result_id", depID, "reason", reason}
	})
}

func (c *Cache) traceExplicitDepRemoved(ctx context.Context, resID, depID sharedResultID, phase string) {
	c.traceLazy(ctx, "explicit_dep_removed", func() []any {
		return []any{"phase", phase, "shared_result_id", resID, "dep_shared_result_id", depID}
	})
}

func (c *Cache) traceEqClassCreated(ctx context.Context, classID eqClassID, dig string) {
	c.traceLazy(ctx, "eq_class_created", func() []any {
		return []any{"phase", "runtime", "eq_class_id", classID, "digest", dig}
	})
}

func (c *Cache) traceEqClassMerged(ctx context.Context, ids []eqClassID, root eqClassID) {
	c.traceLazy(ctx, "eq_class_merged", func() []any {
		return []any{"phase", "runtime", "merge_ids", ids, "root_eq_class_id", root}
	})
}

func (c *Cache) traceTermInputsRepaired(ctx context.Context, termID egraphTermID, oldInputs, newInputs []eqClassID) {
	c.traceLazy(ctx, "term_inputs_repaired", func() []any {
		return []any{"phase", "runtime", "term_id", termID, "old_input_eq_ids", oldInputs, "new_input_eq_ids", newInputs}
	})
}

func (c *Cache) traceTermRehomedUnderEqClasses(ctx context.Context, termID egraphTermID, inputEqIDs []eqClassID) {
	c.traceLazy(ctx, "term_rehomed_under_eq_classes", func() []any {
		return []any{"phase", "runtime", "term_id", termID, "input_eq_ids", inputEqIDs}
	})
}

func (c *Cache) traceTermDigestRecomputed(ctx context.Context, termID egraphTermID, oldTermDigest, newTermDigest string) {
	c.traceLazy(ctx, "term_digest_recomputed", func() []any {
		return []any{"phase", "runtime", "term_id", termID, "old_term_digest", oldTermDigest, "new_term_digest", newTermDigest}
	})
}

func (c *Cache) traceTermOutputsMerged(ctx context.Context, termID, otherTermID egraphTermID, lhs, rhs eqClassID) {
	c.traceLazy(ctx, "term_outputs_merged", func() []any {
		return []any{"phase", "runtime", "term_id", termID, "other_term_id", otherTermID, "lhs_output_eq_id", lhs, "rhs_output_eq_id", rhs}
	})
}

func (c *Cache) traceLookupAttempt(ctx context.Context, requestDigest, selfDigest string, inputDigests []digest.Digest, persistable bool) {
	c.traceLazy(ctx, "lookup_attempt", func() []any {
		return []any{"phase", "runtime", "request_digest", requestDigest, "self_digest", selfDigest, "input_digests", inputDigests, "persistable", persistable}
	})
}

func (c *Cache) traceLookupMissNoMatch(ctx context.Context, requestDigest string, primaryLookupPossible bool, missingInputIndex int, termDigest string, termSetSize int) {
	c.traceLazy(ctx, "lookup_miss_reason", func() []any {
		return []any{"phase", "runtime", "request_digest", requestDigest, "reason", "no_matching_result", "primary_lookup_possible", primaryLookupPossible, "missing_input_index", missingInputIndex, "term_digest", termDigest, "term_set_size", termSetSize}
	})
}

func (c *Cache) traceLookupHit(ctx context.Context, requestDigest string, res *sharedResult, hitTerm *egraphTerm, termDigest string) {
	c.traceLazy(ctx, "lookup_hit", func() []any {
		termID := egraphTermID(0)
		if hitTerm != nil {
			termID = hitTerm.id
		}
		return []any{"phase", "runtime", "request_digest", requestDigest, "shared_result_id", res.id, "term_id", termID, "term_digest", termDigest}
	})
}

func (c *Cache) traceResultCreated(ctx context.Context, res *sharedResult) {
	c.traceLazy(ctx, "result_created", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id, "record_type", res.recordType, "description", res.description}
	})
}

func (c *Cache) tracePersistKeyIndexAdded(ctx context.Context, resID sharedResultID, resultKey string, phase string, importRunID string) {
	c.traceLazy(ctx, "persist_key_index_added", func() []any {
		args := []any{"phase", phase, "shared_result_id", resID, "result_key", resultKey}
		if importRunID != "" {
			args = append(args, "import_run_id", importRunID)
		}
		return args
	})
}

func (c *Cache) tracePersistKeyIndexRemoved(ctx context.Context, resID sharedResultID, resultKey string) {
	c.traceLazy(ctx, "persist_key_index_removed", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "result_key", resultKey}
	})
}

func (c *Cache) traceResultTermAssocUpdated(ctx context.Context, resID sharedResultID, termID egraphTermID, inputProvenance []egraphInputProvenanceKind) {
	c.traceLazy(ctx, "result_term_assoc_updated", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "term_id", termID, "input_provenance", debugInputProvenance(inputProvenance)}
	})
}

func (c *Cache) traceResultTermAssocAdded(ctx context.Context, resID sharedResultID, termID egraphTermID, inputProvenance []egraphInputProvenanceKind) {
	c.traceLazy(ctx, "result_term_assoc_added", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "term_id", termID, "input_provenance", debugInputProvenance(inputProvenance)}
	})
}

func (c *Cache) traceResultTermAssocRemoved(ctx context.Context, resID sharedResultID, termID egraphTermID) {
	c.traceLazy(ctx, "result_term_assoc_removed", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "term_id", termID}
	})
}

func (c *Cache) traceTermCreated(ctx context.Context, phase string, importRunID string, term *egraphTerm) {
	c.traceLazy(ctx, "term_created", func() []any {
		args := []any{"phase", phase, "term_id", term.id, "self_digest", term.selfDigest.String(), "term_digest", term.termDigest, "output_eq_id", term.outputEqID}
		if importRunID != "" {
			args = append(args, "import_run_id", importRunID)
		}
		return args
	})
}

func (c *Cache) traceTermRemoved(ctx context.Context, termID egraphTermID) {
	c.traceLazy(ctx, "term_removed", func() []any {
		return []any{"phase", "runtime", "term_id", termID}
	})
}

func (c *Cache) tracePersistRootMarked(ctx context.Context, resID sharedResultID, rootID sharedResultID, phase, importRunID string) {
	c.traceLazy(ctx, "persist_root_marked", func() []any {
		args := []any{"phase", phase, "shared_result_id", resID, "root_shared_result_id", rootID}
		if importRunID != "" {
			args = append(args, "import_run_id", importRunID)
		}
		return args
	})
}

func (c *Cache) traceResultRemoved(ctx context.Context, res *sharedResult) {
	c.traceLazy(ctx, "result_removed", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id}
	})
}

func (c *Cache) tracePersistBatchAppliedSkip(ctx context.Context, batchID, resultKey, reason string) {
	c.traceLazy(ctx, "persist_batch_applied", func() []any {
		return []any{"phase", "persist-apply", "batch_id", batchID, "result_key", resultKey, "reason", reason}
	})
}

func (c *Cache) tracePersistRootMemberRemoved(ctx context.Context, batchID, rootResultKey, resultKey string) {
	c.traceLazy(ctx, "persist_root_member_removed", func() []any {
		return []any{"phase", "persist-apply", "batch_id", batchID, "root_result_key", rootResultKey, "result_key", resultKey}
	})
}

func (c *Cache) tracePersistRootDeleted(ctx context.Context, batchID, resultKey string) {
	c.traceLazy(ctx, "persist_root_deleted", func() []any {
		return []any{"phase", "persist-apply", "batch_id", batchID, "result_key", resultKey}
	})
}

func (c *Cache) tracePersistOrphanGCDeleted(ctx context.Context, batchID, resultKey string) {
	c.traceLazy(ctx, "persist_orphan_gc_deleted", func() []any {
		return []any{"phase", "persist-apply", "batch_id", batchID, "result_key", resultKey}
	})
}

func (c *Cache) tracePersistBatchApplied(ctx context.Context, batchID, resultKey string) {
	c.traceLazy(ctx, "persist_batch_applied", func() []any {
		return []any{"phase", "persist-apply", "batch_id", batchID, "result_key", resultKey}
	})
}

func (c *Cache) tracePersistRootAdded(ctx context.Context, batchID, resultKey, phase string, importRunID string) {
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

func (c *Cache) tracePersistRootMemberAdded(ctx context.Context, batchID, rootResultKey, resultKey, phase, importRunID string, resID sharedResultID) {
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

func (c *Cache) tracePersistBatchBuildFailed(ctx context.Context, resultKey string, err error) {
	c.traceLazy(ctx, "persist_batch_build_failed", func() []any {
		return []any{"phase", "persist-export", "result_key", resultKey, "error", err.Error()}
	})
}

func (c *Cache) tracePersistBatchBuilt(ctx context.Context, batchID, resultKey string, deleteRoot bool) {
	c.traceLazy(ctx, "persist_batch_built", func() []any {
		return []any{"phase", "persist-export", "batch_id", batchID, "result_key", resultKey, "delete_root", deleteRoot}
	})
}

func (c *Cache) tracePersistWatchCleared(ctx context.Context, rootID, memberID sharedResultID) {
	c.traceLazy(ctx, "persist_watch_cleared", func() []any {
		return []any{"phase", "persist-export", "shared_result_id", rootID, "watched_member_shared_result_id", memberID}
	})
}

func (c *Cache) tracePersistRootDeferred(ctx context.Context, root *sharedResult, memberCount int) {
	c.traceLazy(ctx, "persist_root_deferred", func() []any {
		return []any{"phase", "persist-export", "shared_result_id", root.id, "member_count", memberCount}
	})
}

func (c *Cache) tracePersistMemberNotReady(ctx context.Context, rootID, memberID sharedResultID) {
	c.traceLazy(ctx, "persist_member_not_ready", func() []any {
		return []any{"phase", "persist-export", "shared_result_id", memberID, "root_shared_result_id", rootID}
	})
}

func (c *Cache) traceLazyRealized(ctx context.Context, memberID, rootID sharedResultID) {
	c.traceLazy(ctx, "lazy_realized", func() []any {
		return []any{"phase", "runtime", "shared_result_id", memberID, "root_shared_result_id", rootID}
	})
}

func (c *Cache) tracePersistRetryTriggered(ctx context.Context, resultID sharedResultID) {
	c.traceLazy(ctx, "persist_root_retry_triggered", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resultID}
	})
}

func (c *Cache) tracePersistRetryRegistered(ctx context.Context, resultID sharedResultID) {
	c.traceLazy(ctx, "persist_retry_registered", func() []any {
		return []any{"phase", "persist-export", "shared_result_id", resultID}
	})
}

func (c *Cache) tracePersistRetryNoop(ctx context.Context, rootID sharedResultID) {
	c.traceLazy(ctx, "persist_retry_noop", func() []any {
		return []any{"phase", "runtime", "shared_result_id", rootID}
	})
}

func (c *Cache) tracePersistRetrySucceeded(ctx context.Context, resultID sharedResultID) {
	c.traceLazy(ctx, "persist_retry_succeeded", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resultID}
	})
}

func (c *Cache) tracePersistedPayloadImportedEager(ctx context.Context, importRunID string, resID sharedResultID, resultKey, payloadState string) {
	c.traceLazy(ctx, "persisted_payload_imported_eager", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", resID, "result_key", resultKey, "payload_state", payloadState}
	})
}

func (c *Cache) tracePersistedPayloadImportedLazy(ctx context.Context, importRunID string, resID sharedResultID, resultKey, payloadKind, typeName string) {
	c.traceLazy(ctx, "persisted_payload_imported_lazy", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", resID, "result_key", resultKey, "payload_kind", payloadKind, "type_name", typeName}
	})
}

func (c *Cache) traceImportResultLoaded(ctx context.Context, importRunID string, resID sharedResultID, callFrameJSON string) {
	c.traceLazy(ctx, "import_result_loaded", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", resID, "call_frame_json", callFrameJSON}
	})
}

func (c *Cache) traceImportResultSnapshotLinkLoaded(ctx context.Context, importRunID string, resID sharedResultID, refKey, role, slot string) {
	c.traceLazy(ctx, "import_result_snapshot_link_loaded", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", resID, "ref_key", refKey, "role", role, "slot", slot}
	})
}

func (c *Cache) traceImportResultTermLoaded(ctx context.Context, importRunID string, resID sharedResultID, resultKey string, termID egraphTermID, selfDigest string, inputProvenance []egraphInputProvenanceKind) {
	c.traceLazy(ctx, "import_result_term_loaded", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", resID, "result_key", resultKey, "term_id", termID, "self_digest", selfDigest, "input_provenance", debugInputProvenance(inputProvenance)}
	})
}

func (c *Cache) traceImportResultDepLoaded(ctx context.Context, importRunID string, parentID sharedResultID, depID sharedResultID) {
	c.traceLazy(ctx, "import_result_dep_loaded", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", parentID, "dep_shared_result_id", depID}
	})
}

func (c *Cache) tracePersistedPayloadDecodeFailed(ctx context.Context, res *sharedResult, env *PersistedResultEnvelope, err error) {
	c.traceLazy(ctx, "persisted_payload_decode_failed", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id, "payload_kind", env.Kind, "type_name", env.TypeName, "error", err.Error()}
	})
}

func (c *Cache) tracePersistedPayloadDecoded(ctx context.Context, res *sharedResult, env *PersistedResultEnvelope) {
	c.traceLazy(ctx, "persisted_payload_decoded", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id, "payload_kind", env.Kind, "type_name", env.TypeName}
	})
}

func (c *Cache) DebugEGraphSnapshot() *EGraphDebugSnapshot {
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

		state := res.loadPayloadState()
		typeName := sharedResultObjectTypeName(res, state)
		if typeName == "" && state.self != nil && state.self.Type() != nil {
			typeName = state.self.Type().Name()
		}

		payloadState := "uninitialized"
		switch {
		case state.persistedEnvelope != nil && !state.hasValue:
			payloadState = "imported_lazy_envelope"
		case state.hasValue && state.self == nil:
			payloadState = "nil"
		case state.hasValue:
			payloadState = "materialized"
		}

		snap.Results = append(snap.Results, EGraphDebugResult{
			SharedResultID:         uint64(res.id),
			OutputEqClassIDs:       outputEqIDs,
			OutputEffectIDs:        append([]string(nil), res.outputEffectIDs...),
			RecordType:             res.recordType,
			Description:            res.description,
			TypeName:               typeName,
			IncomingOwnershipCount: res.incomingOwnershipCount,
			HasValue:               state.hasValue,
			PayloadState:           payloadState,
			HasPersistedEdge:       c.persistedEdgesByResult[res.id].resultID != 0,
			ExplicitDeps:           depIDs,
			HeldDependencyResults:  len(res.deps),
			SnapshotLinks:          links,
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

func (c *Cache) WriteDebugCacheSnapshot(w io.Writer) error {
	c.callsMu.Lock()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	defer c.callsMu.Unlock()

	bw := bufio.NewWriterSize(w, 128<<10)

	writeValue := func(v any) error {
		bs, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = bw.Write(bs)
		return err
	}

	topFieldCount := 0
	writeField := func(name string) error {
		if topFieldCount > 0 {
			if _, err := bw.WriteString(","); err != nil {
				return err
			}
		}
		topFieldCount++
		if err := writeValue(name); err != nil {
			return err
		}
		_, err := bw.WriteString(":")
		return err
	}

	writeArrayField := func(name string, writeElems func(func(any) error) error) error {
		if err := writeField(name); err != nil {
			return err
		}
		if _, err := bw.WriteString("["); err != nil {
			return err
		}
		elemCount := 0
		if err := writeElems(func(v any) error {
			if elemCount > 0 {
				if _, err := bw.WriteString(","); err != nil {
					return err
				}
			}
			elemCount++
			return writeValue(v)
		}); err != nil {
			return err
		}
		_, err := bw.WriteString("]")
		return err
	}

	if _, err := bw.WriteString("{"); err != nil {
		return err
	}
	if err := writeField("trace_format_version"); err != nil {
		return err
	}
	if err := writeValue(egraphTraceFormatV1); err != nil {
		return err
	}
	if err := writeField("boot_id"); err != nil {
		return err
	}
	if err := writeValue(c.traceBootID); err != nil {
		return err
	}
	if err := writeField("captured_at_seq"); err != nil {
		return err
	}
	if err := writeValue(atomic.LoadUint64(&c.traceSeq)); err != nil {
		return err
	}
	if err := writeField("captured_at_time"); err != nil {
		return err
	}
	if err := writeValue(time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}

	resultIDs := make([]sharedResultID, 0, len(c.resultsByID))
	for resultID := range c.resultsByID {
		resultIDs = append(resultIDs, resultID)
	}
	slices.Sort(resultIDs)

	resultIndexDigests := make([]string, 0, len(c.egraphResultsByDigest))
	for dig := range c.egraphResultsByDigest {
		resultIndexDigests = append(resultIndexDigests, dig)
	}
	slices.Sort(resultIndexDigests)
	indexedDigestsByResult := make(map[sharedResultID][]string, len(resultIDs))
	for _, dig := range resultIndexDigests {
		for resultID := range c.egraphResultsByDigest[dig] {
			indexedDigestsByResult[resultID] = append(indexedDigestsByResult[resultID], dig)
		}
	}

	if err := writeArrayField("results", func(writeElem func(any) error) error {
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

			state := res.loadPayloadState()
			typeName := sharedResultObjectTypeName(res, state)
			if typeName == "" && state.self != nil && state.self.Type() != nil {
				typeName = state.self.Type().Name()
			}

			payloadState := "uninitialized"
			switch {
			case state.persistedEnvelope != nil && !state.hasValue:
				payloadState = "imported_lazy_envelope"
			case state.hasValue && state.self == nil:
				payloadState = "nil"
			case state.hasValue:
				payloadState = "materialized"
			}

			var recipeDigest string
			var recipeDigestErr string
			var contentPreferredDigest string
			var contentPreferredDigestErr string
			var inputDigests []string
			var inputDigestsErr string
			frame := res.loadResultCall()
			if frame != nil {
				if dig, err := frame.deriveRecipeDigest(c); err == nil {
					recipeDigest = dig.String()
				} else {
					recipeDigestErr = err.Error()
				}
				if dig, err := frame.deriveContentPreferredDigest(c); err == nil {
					contentPreferredDigest = dig.String()
				} else {
					contentPreferredDigestErr = err.Error()
				}
				if digs, err := frame.inputs(c); err == nil {
					inputDigests = make([]string, 0, len(digs))
					for _, dig := range digs {
						inputDigests = append(inputDigests, dig.String())
					}
				} else {
					inputDigestsErr = err.Error()
				}
			}

			assocTermIDs := make([]uint64, 0, len(c.termIDsForResultLocked(resultID)))
			for termID := range c.termIDsForResultLocked(resultID) {
				assocTermIDs = append(assocTermIDs, uint64(termID))
			}
			slices.Sort(assocTermIDs)

			persistedEnvelopeKind := ""
			persistedEnvelopeTypeName := ""
			if state.persistedEnvelope != nil {
				persistedEnvelopeKind = state.persistedEnvelope.Kind
				persistedEnvelopeTypeName = state.persistedEnvelope.TypeName
			}

			if err := writeElem(CacheDebugResult{
				EGraphDebugResult: EGraphDebugResult{
					SharedResultID:         uint64(res.id),
					OutputEqClassIDs:       outputEqIDs,
					OutputEffectIDs:        append([]string(nil), res.outputEffectIDs...),
					RecordType:             res.recordType,
					Description:            res.description,
					TypeName:               typeName,
					IncomingOwnershipCount: res.incomingOwnershipCount,
					HasValue:               state.hasValue,
					PayloadState:           payloadState,
					HasPersistedEdge:       c.persistedEdgesByResult[res.id].resultID != 0,
					ExplicitDeps:           depIDs,
					HeldDependencyResults:  len(res.deps),
					SnapshotLinks:          links,
				},
				ResultCall:                            frame,
				ResultCallRecipeDigest:                recipeDigest,
				ResultCallRecipeDigestError:           recipeDigestErr,
				ResultCallContentPreferredDigest:      contentPreferredDigest,
				ResultCallContentPreferredDigestError: contentPreferredDigestErr,
				ResultCallInputDigests:                inputDigests,
				ResultCallInputDigestsError:           inputDigestsErr,
				AssociatedTermIDs:                     assocTermIDs,
				IndexedDigests:                        append([]string(nil), indexedDigestsByResult[resultID]...),
				SafeToPersistCache:                    res.safeToPersistCache,
				ExpiresAtUnix:                         res.expiresAtUnix,
				CreatedAtUnixNano:                     state.createdAtUnixNano,
				LastUsedAtUnixNano:                    state.lastUsedAtUnixNano,
				SizeEstimateBytes:                     res.sizeEstimateBytes,
				UsageIdentity:                         res.usageIdentity,
				PersistedEnvelopeKind:                 persistedEnvelopeKind,
				PersistedEnvelopeTypeName:             persistedEnvelopeTypeName,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if err := writeArrayField("result_digest_indexes", func(writeElem func(any) error) error {
		for _, dig := range resultIndexDigests {
			resultSet := c.egraphResultsByDigest[dig]
			if len(resultSet) == 0 {
				continue
			}
			indexedResultIDs := make([]uint64, 0, len(resultSet))
			for resultID := range resultSet {
				indexedResultIDs = append(indexedResultIDs, uint64(resultID))
			}
			slices.Sort(indexedResultIDs)
			if err := writeElem(CacheDebugResultDigestIndex{
				Digest:          dig,
				SharedResultIDs: indexedResultIDs,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	termIDs := make([]egraphTermID, 0, len(c.egraphTerms))
	for termID := range c.egraphTerms {
		termIDs = append(termIDs, termID)
	}
	slices.Sort(termIDs)
	if err := writeArrayField("terms", func(writeElem func(any) error) error {
		for _, termID := range termIDs {
			term := c.egraphTerms[termID]
			if term == nil {
				continue
			}
			inputEqIDs := make([]uint64, 0, len(term.inputEqIDs))
			for _, in := range term.inputEqIDs {
				inputEqIDs = append(inputEqIDs, uint64(in))
			}
			if err := writeElem(EGraphDebugTerm{
				TermID:     uint64(term.id),
				SelfDigest: term.selfDigest.String(),
				InputEqIDs: inputEqIDs,
				TermDigest: term.termDigest,
				OutputEqID: uint64(term.outputEqID),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if err := writeArrayField("result_terms", func(writeElem func(any) error) error {
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
					inputProvenance = append(inputProvenance, EGraphDebugInputProvenance{Kind: string(input)})
				}
				if err := writeElem(EGraphDebugResultTerm{
					SharedResultID:  uint64(resultID),
					TermID:          uint64(termID),
					InputProvenance: inputProvenance,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}

	digests := make([]string, 0, len(c.egraphDigestToClass))
	for dig := range c.egraphDigestToClass {
		digests = append(digests, dig)
	}
	slices.Sort(digests)
	classMembers := make(map[eqClassID][]string)
	if err := writeArrayField("digests", func(writeElem func(any) error) error {
		for _, dig := range digests {
			root := c.findEqClassLocked(c.egraphDigestToClass[dig])
			classMembers[root] = append(classMembers[root], dig)
			if err := writeElem(EGraphDebugDigestMapping{
				Digest:    dig,
				EqClassID: uint64(root),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	classIDs := make([]eqClassID, 0, len(classMembers))
	for classID := range classMembers {
		classIDs = append(classIDs, classID)
	}
	slices.Sort(classIDs)
	if err := writeArrayField("eq_classes", func(writeElem func(any) error) error {
		for _, classID := range classIDs {
			members := append([]string(nil), classMembers[classID]...)
			slices.Sort(members)
			if err := writeElem(EGraphDebugEqClass{
				EqClassID: uint64(classID),
				Digests:   members,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if len(c.ongoingCalls) > 0 {
		if err := writeArrayField("ongoing_calls", func(writeElem func(any) error) error {
			keys := make([]callConcurrencyKeys, 0, len(c.ongoingCalls))
			for key := range c.ongoingCalls {
				keys = append(keys, key)
			}
			slices.SortFunc(keys, func(a, b callConcurrencyKeys) int {
				switch {
				case a.callKey < b.callKey:
					return -1
				case a.callKey > b.callKey:
					return 1
				case a.concurrencyKey < b.concurrencyKey:
					return -1
				case a.concurrencyKey > b.concurrencyKey:
					return 1
				default:
					return 0
				}
			})
			for _, key := range keys {
				call := c.ongoingCalls[key]
				if call == nil {
					continue
				}
				completed := false
				select {
				case <-call.waitCh:
					completed = true
				default:
				}
				entry := CacheDebugOngoingCall{
					CallKey:        key.callKey,
					ConcurrencyKey: key.concurrencyKey,
					Waiters:        call.waiters,
					IsPersistable:  call.isPersistable,
					TTLSeconds:     call.ttlSeconds,
					Completed:      completed,
				}
				if call.err != nil {
					entry.Error = call.err.Error()
				}
				if call.res != nil {
					entry.SharedResultID = uint64(call.res.id)
					entry.ResultDescription = call.res.description
					entry.ResultRecordType = call.res.recordType
					state := call.res.loadPayloadState()
					if typeName := sharedResultObjectTypeName(call.res, state); typeName != "" {
						entry.ResultTypeName = typeName
					} else if state.self != nil && state.self.Type() != nil {
						entry.ResultTypeName = state.self.Type().Name()
					}
				}
				if err := writeElem(entry); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	if len(c.ongoingArbitraryCalls) > 0 {
		if err := writeArrayField("ongoing_arbitrary_calls", func(writeElem func(any) error) error {
			keys := make([]string, 0, len(c.ongoingArbitraryCalls))
			for key := range c.ongoingArbitraryCalls {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				call := c.ongoingArbitraryCalls[key]
				if call == nil {
					continue
				}
				completed := false
				select {
				case <-call.waitCh:
					completed = true
				default:
				}
				entry := CacheDebugArbitraryCall{
					CallKey:           key,
					Waiters:           call.waiters,
					OwnerSessionCount: call.ownerSessionCount,
					Completed:         completed,
					HasValue:          call.value != nil,
				}
				if call.value != nil {
					entry.ValueType = fmt.Sprintf("%T", call.value)
				}
				if call.err != nil {
					entry.Error = call.err.Error()
				}
				if err := writeElem(entry); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	if len(c.completedArbitraryCalls) > 0 {
		if err := writeArrayField("completed_arbitrary_calls", func(writeElem func(any) error) error {
			keys := make([]string, 0, len(c.completedArbitraryCalls))
			for key := range c.completedArbitraryCalls {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				call := c.completedArbitraryCalls[key]
				if call == nil {
					continue
				}
				entry := CacheDebugArbitraryCall{
					CallKey:           key,
					Waiters:           call.waiters,
					OwnerSessionCount: call.ownerSessionCount,
					Completed:         true,
					HasValue:          call.value != nil,
				}
				if call.value != nil {
					entry.ValueType = fmt.Sprintf("%T", call.value)
				}
				if call.err != nil {
					entry.Error = call.err.Error()
				}
				if err := writeElem(entry); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	if _, err := bw.WriteString("}"); err != nil {
		return err
	}
	return bw.Flush()
}
