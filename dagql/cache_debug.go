package dagql

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	SessionResults          []CacheDebugSessionResults    `json:"session_results,omitempty"`
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
	SharedResultID           uint64                     `json:"shared_result_id"`
	OutputEqClassIDs         []uint64                   `json:"output_eq_class_ids,omitempty"`
	RecordType               string                     `json:"record_type,omitempty"`
	Description              string                     `json:"description,omitempty"`
	TypeName                 string                     `json:"type_name,omitempty"`
	IncomingOwnershipCount   int64                      `json:"incoming_ownership_count"`
	HasValue                 bool                       `json:"has_value"`
	PayloadState             string                     `json:"payload_state"`
	HasPersistedEdge         bool                       `json:"has_persisted_edge"`
	PersistedEdgeUnpruneable bool                       `json:"persisted_edge_unpruneable"`
	ExplicitDeps             []uint64                   `json:"explicit_dep_ids,omitempty"`
	HeldDependencyResults    int                        `json:"held_dependency_results_count"`
	SnapshotLinks            []PersistedSnapshotRefLink `json:"snapshot_links,omitempty"`
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

type CacheDebugSessionResults struct {
	SessionID       string   `json:"session_id"`
	SharedResultIDs []uint64 `json:"shared_result_ids"`
}

func newTraceBootID() string {
	if bootID := os.Getenv("_DAGGER_EGRAPH_BOOT_ID"); bootID != "" {
		return bootID
	}
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

func debugResultCallSummary(frame *ResultCall) (field string, kind string, typeName string) {
	if frame == nil {
		return "", "", ""
	}
	kind = string(frame.Kind)
	if resolvedField, err := resultCallIdentityField(frame); err == nil {
		field = resolvedField
	}
	if frame.Type != nil {
		typeName = frame.Type.NamedType
	}
	return field, kind, typeName
}

func (c *Cache) debugSessionResultsSnapshot() []CacheDebugSessionResults {
	if c == nil {
		return nil
	}
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if len(c.sessionResultIDsBySession) == 0 {
		return nil
	}
	sessionIDs := make([]string, 0, len(c.sessionResultIDsBySession))
	for sessionID := range c.sessionResultIDsBySession {
		sessionIDs = append(sessionIDs, sessionID)
	}
	slices.Sort(sessionIDs)
	out := make([]CacheDebugSessionResults, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		resultIDs := c.sessionResultIDsBySession[sessionID]
		if len(resultIDs) == 0 {
			continue
		}
		sharedResultIDs := make([]uint64, 0, len(resultIDs))
		for resultID := range resultIDs {
			sharedResultIDs = append(sharedResultIDs, uint64(resultID))
		}
		slices.Sort(sharedResultIDs)
		out = append(out, CacheDebugSessionResults{
			SessionID:       sessionID,
			SharedResultIDs: sharedResultIDs,
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

func TraceEGraphDebug(ctx context.Context, event string, args ...any) {
	if !debugEGraphTrace {
		return
	}
	cache, err := EngineCache(ctx)
	if err != nil || cache == nil {
		return
	}
	cache.trace(ctx, event, args...)
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

func (c *Cache) tracePruneCandidateSkipped(ctx context.Context, policyIndex int, reason string, res pruneSnapshotResult) {
	c.traceLazy(ctx, "prune_candidate_skipped", func() []any {
		return []any{
			"phase", "prune",
			"policy_index", policyIndex,
			"reason", reason,
			"shared_result_id", res.resultID,
			"record_type", res.entry.RecordType,
			"description", res.entry.Description,
			"usage_identity", res.usageIdentity,
			"incoming_ownership_count", res.incomingCount,
			"has_persisted_edge", res.hasPersistedEdge,
			"persisted_edge_unpruneable", res.persistedEdgeUnpruneable,
			"expires_at_unix", res.expiresAtUnix,
			"created_time_unix_nano", res.entry.CreatedTimeUnixNano,
			"most_recent_use_time_unix_nano", res.entry.MostRecentUseTimeUnixNano,
			"size_bytes", res.entry.SizeBytes,
			"dep_count", len(res.deps),
			"actively_used", res.entry.ActivelyUsed,
		}
	})
}

func (c *Cache) tracePruneCandidateSelected(ctx context.Context, policyIndex int, candidate pruneCandidate, res pruneSnapshotResult, reclaimBytes int64) {
	c.traceLazy(ctx, "prune_candidate_selected", func() []any {
		return []any{
			"phase", "prune",
			"policy_index", policyIndex,
			"shared_result_id", candidate.resultID,
			"record_type", res.entry.RecordType,
			"description", res.entry.Description,
			"usage_identity", res.usageIdentity,
			"incoming_ownership_count", res.incomingCount,
			"has_persisted_edge", res.hasPersistedEdge,
			"persisted_edge_unpruneable", res.persistedEdgeUnpruneable,
			"expires_at_unix", candidate.expiresAtUnix,
			"created_time_unix_nano", res.entry.CreatedTimeUnixNano,
			"most_recent_use_time_unix_nano", res.entry.MostRecentUseTimeUnixNano,
			"size_bytes", res.entry.SizeBytes,
			"dep_count", len(res.deps),
			"reclaim_bytes", reclaimBytes,
			"actively_used", res.entry.ActivelyUsed,
		}
	})
}

func (c *Cache) tracePrunePersistedEdgeRemoved(ctx context.Context, policyIndex int, candidate pruneCandidate, res pruneSnapshotResult, reclaimBytes int64) {
	c.traceLazy(ctx, "prune_persisted_edge_removed", func() []any {
		return []any{
			"phase", "prune",
			"policy_index", policyIndex,
			"shared_result_id", candidate.resultID,
			"record_type", res.entry.RecordType,
			"description", res.entry.Description,
			"usage_identity", res.usageIdentity,
			"incoming_ownership_count", res.incomingCount,
			"has_persisted_edge", res.hasPersistedEdge,
			"persisted_edge_unpruneable", res.persistedEdgeUnpruneable,
			"expires_at_unix", candidate.expiresAtUnix,
			"created_time_unix_nano", res.entry.CreatedTimeUnixNano,
			"most_recent_use_time_unix_nano", res.entry.MostRecentUseTimeUnixNano,
			"size_bytes", res.entry.SizeBytes,
			"dep_count", len(res.deps),
			"reclaim_bytes", reclaimBytes,
			"actively_used", res.entry.ActivelyUsed,
		}
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

func (c *Cache) traceSessionResultTracked(ctx context.Context, sessionID string, res AnyResult, hitCache bool, trackedCount int) {
	c.traceLazy(ctx, "session_result_tracked", func() []any {
		shared := res.cacheSharedResult()
		args := []any{
			"phase", "runtime",
			"session_id", sessionID,
			"hit_cache", hitCache,
			"tracked_count", trackedCount,
		}
		if shared != nil {
			args = append(args,
				"shared_result_id", shared.id,
				"incoming_ownership_count", shared.incomingOwnershipCount,
				"record_type", shared.recordType,
				"description", shared.description,
				"dep_count", len(shared.deps),
			)
			if frame := shared.loadResultCall(); frame != nil {
				field, kind, typeName := debugResultCallSummary(frame)
				args = append(args,
					"has_result_call", true,
					"result_call_field", field,
					"result_call_kind", kind,
					"result_call_type", typeName,
				)
			} else {
				args = append(args, "has_result_call", false)
			}
		}
		return args
	})
}

func (c *Cache) traceSessionResultReleasing(ctx context.Context, sessionID string, res AnyResult, reason string, releaseIndex, totalTracked int) {
	c.traceLazy(ctx, "session_result_releasing", func() []any {
		shared := res.cacheSharedResult()
		args := []any{
			"phase", "runtime",
			"reason", reason,
			"session_id", sessionID,
			"release_index", releaseIndex,
			"total_tracked", totalTracked,
		}
		if shared != nil {
			args = append(args,
				"shared_result_id", shared.id,
				"incoming_ownership_count", shared.incomingOwnershipCount,
				"record_type", shared.recordType,
				"description", shared.description,
				"dep_count", len(shared.deps),
			)
			if frame := shared.loadResultCall(); frame != nil {
				field, kind, typeName := debugResultCallSummary(frame)
				args = append(args,
					"has_result_call", true,
					"result_call_field", field,
					"result_call_kind", kind,
					"result_call_type", typeName,
				)
			} else {
				args = append(args, "has_result_call", false)
			}
		}
		return args
	})
}

func (c *Cache) traceExplicitDepAdded(ctx context.Context, resID, depID sharedResultID, reason string) {
	c.traceLazy(ctx, "explicit_dep_added", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "dep_shared_result_id", depID, "reason", reason}
	})
}

func (c *Cache) traceResultCallDepAdded(ctx context.Context, resID, depID sharedResultID, path string) {
	c.traceLazy(ctx, "result_call_dep_added", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "dep_shared_result_id", depID, "path", path}
	})
}

func (c *Cache) traceDependencyRemoved(ctx context.Context, resID, depID sharedResultID, reason string) {
	c.traceLazy(ctx, "dependency_removed", func() []any {
		return []any{"phase", "runtime", "shared_result_id", resID, "dep_shared_result_id", depID, "reason", reason}
	})
}

func (c *Cache) traceEqClassCreated(ctx context.Context, classID eqClassID, dig string) {
	c.traceLazy(ctx, "eq_class_created", func() []any {
		return []any{"phase", "runtime", "eq_class_id", classID, "digest", dig}
	})
}

func (c *Cache) traceEqClassMerged(ctx context.Context, ids []eqClassID, root eqClassID) {
	c.traceLazy(ctx, "eq_class_merged", func() []any {
		return []any{
			"phase", "runtime",
			"merge_ids", ids,
			"root_eq_class_id", root,
			"merge_eq_class_details", c.debugEqClassMergeDetails(ids),
		}
	})
}

func debugExtraDigestsForTrace(extras map[call.ExtraDigest]struct{}) []string {
	if len(extras) == 0 {
		return nil
	}
	out := make([]string, 0, len(extras))
	for extra := range extras {
		label := extra.Label
		if label == "" {
			label = "unlabeled"
		}
		out = append(out, fmt.Sprintf("%s=%s", label, extra.Digest))
	}
	slices.Sort(out)
	return out
}

func (c *Cache) debugEqClassMergeDetails(ids []eqClassID) []map[string]any {
	if c == nil || len(ids) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		root := c.findEqClassLocked(id)
		if root == 0 {
			continue
		}
		digests := make([]string, 0, len(c.eqClassToDigests[root]))
		for dig := range c.eqClassToDigests[root] {
			digests = append(digests, dig)
		}
		slices.Sort(digests)
		out = append(out, map[string]any{
			"eq_class_id":     root,
			"digests":         digests,
			"extra_digests":   debugExtraDigestsForTrace(c.eqClassExtraDigests[root]),
			"input_term_ids":  c.debugEqClassTermIDs(c.inputEqClassToTerms[root]),
			"output_term_ids": c.debugEqClassTermIDs(c.outputEqClassToTerms[root]),
		})
	}
	return out
}

func (c *Cache) debugEqClassTermIDs(terms map[egraphTermID]struct{}) []uint64 {
	if len(terms) == 0 {
		return nil
	}
	out := make([]uint64, 0, len(terms))
	for termID := range terms {
		out = append(out, uint64(termID))
	}
	slices.Sort(out)
	return out
}

func (c *Cache) traceTeachContentDigest(ctx context.Context, res *sharedResult, oldContentDigest, newContentDigest, requestDigest, requestSelf string, requestInputs []digest.Digest, frame *ResultCall) {
	c.traceLazy(ctx, "teach_content_digest", func() []any {
		args := []any{
			"phase", "runtime",
			"shared_result_id", res.id,
			"record_type", res.recordType,
			"description", res.description,
			"old_content_digest", oldContentDigest,
			"new_content_digest", newContentDigest,
			"request_digest", requestDigest,
			"request_self_digest", requestSelf,
			"request_input_digests", requestInputs,
		}
		if frame != nil {
			field, kind, typeName := debugResultCallSummary(frame)
			args = append(args,
				"result_call_field", field,
				"result_call_kind", kind,
				"result_call_type", typeName,
			)
			if len(frame.ExtraDigests) > 0 {
				extras := make([]string, 0, len(frame.ExtraDigests))
				for _, extra := range frame.ExtraDigests {
					label := extra.Label
					if label == "" {
						label = "unlabeled"
					}
					extras = append(extras, fmt.Sprintf("%s=%s", label, extra.Digest))
				}
				slices.Sort(extras)
				args = append(args, "request_extra_digests", extras)
			}
		}
		return args
	})
}

func (c *Cache) traceTeachResultIdentityRootSet(ctx context.Context, res *sharedResult, requestDigest, requestSelf string, requestInputs []digest.Digest, requestFrame *ResultCall, mergeIDs []eqClassID) {
	c.traceLazy(ctx, "teach_result_identity_root_set", func() []any {
		args := []any{
			"phase", "runtime",
			"shared_result_id", res.id,
			"record_type", res.recordType,
			"description", res.description,
			"request_digest", requestDigest,
			"request_self_digest", requestSelf,
			"request_input_digests", requestInputs,
			"merge_eq_class_ids", mergeIDs,
			"merge_eq_class_details", c.debugEqClassMergeDetails(mergeIDs),
		}
		if requestFrame != nil {
			field, kind, typeName := debugResultCallSummary(requestFrame)
			args = append(args,
				"result_call_field", field,
				"result_call_kind", kind,
				"result_call_type", typeName,
			)
		}
		return args
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

func (c *Cache) traceLookupHit(ctx context.Context, requestDigest string, res *sharedResult, termDigest string) {
	c.traceLazy(ctx, "lookup_hit", func() []any {
		return []any{"phase", "runtime", "request_digest", requestDigest, "shared_result_id", res.id, "term_digest", termDigest}
	})
}

func (c *Cache) traceResultCreated(ctx context.Context, res *sharedResult) {
	c.traceLazy(ctx, "result_created", func() []any {
		return []any{"phase", "runtime", "shared_result_id", res.id, "record_type", res.recordType, "description", res.description}
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

func (c *Cache) traceImportResultDepLoaded(ctx context.Context, importRunID string, parentID sharedResultID, depID sharedResultID) {
	c.traceLazy(ctx, "import_result_dep_loaded", func() []any {
		return []any{"phase", "import", "import_run_id", importRunID, "shared_result_id", parentID, "dep_shared_result_id", depID}
	})
}

func (c *Cache) traceResultCallFrameUpdated(ctx context.Context, res *sharedResult, reason string, oldFrame, newFrame *ResultCall) {
	c.traceLazy(ctx, "result_call_frame_updated", func() []any {
		oldField, oldKind, oldType := debugResultCallSummary(oldFrame)
		newField, newKind, newType := debugResultCallSummary(newFrame)
		return []any{
			"phase", "runtime",
			"reason", reason,
			"shared_result_id", res.id,
			"record_type", res.recordType,
			"description", res.description,
			"incoming_ownership_count", res.incomingOwnershipCount,
			"dep_count", len(res.deps),
			"old_frame_present", oldFrame != nil,
			"old_frame_field", oldField,
			"old_frame_kind", oldKind,
			"old_frame_type", oldType,
			"new_frame_present", newFrame != nil,
			"new_frame_field", newField,
			"new_frame_kind", newKind,
			"new_frame_type", newType,
		}
	})
}

func (c *Cache) traceResultRemoved(ctx context.Context, res *sharedResult, oldFrame *ResultCall, depCount int) {
	c.traceLazy(ctx, "result_removed", func() []any {
		field, kind, typeName := debugResultCallSummary(oldFrame)
		return []any{
			"phase", "runtime",
			"shared_result_id", res.id,
			"record_type", res.recordType,
			"description", res.description,
			"incoming_ownership_count", res.incomingOwnershipCount,
			"dep_count", depCount,
			"had_result_call", oldFrame != nil,
			"result_call_field", field,
			"result_call_kind", kind,
			"result_call_type", typeName,
		}
	})
}

func (c *Cache) traceAttachResultReusedCacheBacked(ctx context.Context, sessionID string, shared *sharedResult) {
	c.traceLazy(ctx, "attach_result_reused_cache_backed", func() []any {
		frame := shared.loadResultCall()
		field, kind, typeName := debugResultCallSummary(frame)
		return []any{
			"phase", "runtime",
			"session_id", sessionID,
			"shared_result_id", shared.id,
			"record_type", shared.recordType,
			"description", shared.description,
			"incoming_ownership_count", shared.incomingOwnershipCount,
			"dep_count", len(shared.deps),
			"has_result_call", frame != nil,
			"result_call_field", field,
			"result_call_kind", kind,
			"result_call_type", typeName,
		}
	})
}

func (c *Cache) traceRecipeIDRebuildFailed(ctx context.Context, rootFrame *ResultCall, ref *ResultCallRef, reason string) {
	c.traceLazy(ctx, "recipe_id_rebuild_failed", func() []any {
		rootField, rootKind, rootType := debugResultCallSummary(rootFrame)
		args := []any{
			"phase", "runtime",
			"reason", reason,
			"root_call_field", rootField,
			"root_call_kind", rootKind,
			"root_call_type", rootType,
		}
		if ref == nil {
			return args
		}
		args = append(args, "failing_result_id", ref.ResultID)
		if ref.shared != nil {
			args = append(args,
				"ref_shared_present", true,
				"ref_shared_has_frame", ref.shared.loadResultCall() != nil,
			)
		} else {
			args = append(args, "ref_shared_present", false)
		}
		if c == nil {
			args = append(args, "cache_present", false)
			return args
		}
		c.egraphMu.RLock()
		res := c.resultsByID[sharedResultID(ref.ResultID)]
		if res != nil {
			frame := res.loadResultCall()
			field, kind, typeName := debugResultCallSummary(frame)
			args = append(args,
				"cache_result_present", true,
				"cache_result_has_frame", frame != nil,
				"cache_result_record_type", res.recordType,
				"cache_result_description", res.description,
				"cache_result_incoming_ownership_count", res.incomingOwnershipCount,
				"cache_result_dep_count", len(res.deps),
				"cache_result_call_field", field,
				"cache_result_call_kind", kind,
				"cache_result_call_type", typeName,
			)
		} else {
			args = append(args, "cache_result_present", false)
		}
		c.egraphMu.RUnlock()
		return args
	})
}

func (c *Cache) traceResultCallRefFromResultFailed(ctx context.Context, res AnyResult, reason string) {
	c.traceLazy(ctx, "result_call_ref_from_result_failed", func() []any {
		args := []any{
			"phase", "runtime",
			"reason", reason,
			"result_concrete_type", fmt.Sprintf("%T", res),
		}
		if res == nil {
			return args
		}
		if typ := res.Type(); typ != nil {
			args = append(args, "result_type_name", typ.NamedType)
		}
		shared := res.cacheSharedResult()
		if shared == nil {
			args = append(args, "shared_present", false)
			return args
		}
		args = append(args,
			"shared_present", true,
			"shared_result_id", shared.id,
			"shared_attached", shared.id != 0,
			"shared_has_frame", shared.loadResultCall() != nil,
		)
		if c == nil || shared.id == 0 {
			return args
		}
		c.egraphMu.RLock()
		cacheRes := c.resultsByID[shared.id]
		if cacheRes != nil {
			frame := cacheRes.loadResultCall()
			field, kind, typeName := debugResultCallSummary(frame)
			args = append(args,
				"cache_result_present", true,
				"cache_result_has_frame", frame != nil,
				"cache_result_record_type", cacheRes.recordType,
				"cache_result_description", cacheRes.description,
				"cache_result_incoming_ownership_count", cacheRes.incomingOwnershipCount,
				"cache_result_dep_count", len(cacheRes.deps),
				"cache_result_call_field", field,
				"cache_result_call_kind", kind,
				"cache_result_call_type", typeName,
			)
		} else {
			args = append(args, "cache_result_present", false)
		}
		c.egraphMu.RUnlock()
		return args
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
		links := append([]PersistedSnapshotRefLink(nil), res.snapshotOwnerLinks...)
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
			SharedResultID:           uint64(res.id),
			OutputEqClassIDs:         outputEqIDs,
			RecordType:               res.recordType,
			Description:              res.description,
			TypeName:                 typeName,
			IncomingOwnershipCount:   res.incomingOwnershipCount,
			HasValue:                 state.hasValue,
			PayloadState:             payloadState,
			HasPersistedEdge:         c.persistedEdgesByResult[res.id].resultID != 0,
			PersistedEdgeUnpruneable: c.persistedEdgesByResult[res.id].unpruneable,
			ExplicitDeps:             depIDs,
			HeldDependencyResults:    len(res.deps),
			SnapshotLinks:            links,
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
	sessionResults := c.debugSessionResultsSnapshot()
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
	if err := writeArrayField("session_results", func(writeElem func(any) error) error {
		for _, sessionResult := range sessionResults {
			if err := writeElem(sessionResult); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
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
		for resultID := range c.egraphResultsByDigest[dig].Items() {
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

			links := append([]PersistedSnapshotRefLink(nil), res.snapshotOwnerLinks...)
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
					SharedResultID:           uint64(res.id),
					OutputEqClassIDs:         outputEqIDs,
					RecordType:               res.recordType,
					Description:              res.description,
					TypeName:                 typeName,
					IncomingOwnershipCount:   res.incomingOwnershipCount,
					HasValue:                 state.hasValue,
					PayloadState:             payloadState,
					HasPersistedEdge:         c.persistedEdgesByResult[res.id].resultID != 0,
					PersistedEdgeUnpruneable: c.persistedEdgesByResult[res.id].unpruneable,
					ExplicitDeps:             depIDs,
					HeldDependencyResults:    len(res.deps),
					SnapshotLinks:            links,
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
			if resultSet == nil || resultSet.Empty() {
				continue
			}
			indexedResultIDs := make([]uint64, 0, resultSet.Size())
			for resultID := range resultSet.Items() {
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
