package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func (srv *Server) finalizeSessionArchive(ctx context.Context, sess *daggerSession) (rerr error) {
	manifest := sess.archiveManifest
	if manifest == nil {
		return nil
	}
	if err := srv.archives.BeginFinalizing(manifest.TraceID, manifest.Generation); err != nil {
		return err
	}
	defer func() {
		if rerr != nil {
			_ = srv.archives.MarkIncomplete(manifest.TraceID, manifest.Generation, rerr)
		}
	}()
	if sess.archiveIncomplete.Load() {
		if err := sess.archiveFailure(); err != nil {
			return err
		}
		return errors.New("archive trace validation failed")
	}

	db, err := srv.clientDBs.Open(ctx, manifest.MainClientID)
	if err != nil {
		return fmt.Errorf("open main telemetry store: %w", err)
	}
	defer db.Close()
	cut, err := db.Checkpoint(ctx)
	if err != nil {
		return fmt.Errorf("checkpoint telemetry store: %w", err)
	}
	if err := db.ValidateTraceCut(ctx, manifest.TraceID, cut); err != nil {
		return fmt.Errorf("validate archive trace: %w", err)
	}
	storeSize, err := db.SizeBytes()
	if err != nil {
		return fmt.Errorf("measure telemetry store: %w", err)
	}

	sealAt := time.Now().UTC()
	bootstrapBytes, records, hasAgents, err := buildArchiveBootstrap(ctx, db, *manifest, cut, sealAt)
	if err != nil {
		return err
	}
	if !hasAgents {
		// Opted-in commands that never published an agent identity do not create
		// empty picker entries.
		return srv.archives.Discard(manifest.TraceID, manifest.Generation)
	}
	_, err = srv.archives.Finalize(manifest.TraceID, manifest.Generation, archive.FinalizeInput{
		HighWater: archive.HighWater{Spans: cut.Spans, Logs: cut.Logs, Metrics: cut.Metrics},
		SealAt:    sealAt, StoreSizeBytes: storeSize,
		BootstrapBytes: bootstrapBytes, BootstrapRecords: records,
	})
	return err
}

func buildArchiveBootstrap(ctx context.Context, db *clientdb.DB, manifest archive.Manifest, cut clientdb.HighWater, sealAt time.Time) ([]byte, int64, bool, error) {
	identityIDs := db.AgentIdentitySpanIDs(manifest.TraceID)
	finals, err := selectFinalAgentCheckpoints(ctx, db, manifest.TraceID, cut.Logs)
	if err != nil {
		return nil, 0, false, fmt.Errorf("verify final agent checkpoints: %w", err)
	}
	if len(finals) == 0 {
		if len(identityIDs) == 0 {
			return nil, 0, false, nil
		}
		return nil, 0, false, errors.New("agent identity spans exist without authoritative final checkpoints")
	}

	spanIDs := db.AncestorClosureForTrace(manifest.TraceID, stringSliceSet(identityIDs))
	spans, err := db.SelectSpansLatestForTrace(ctx, manifest.TraceID, spanIDs)
	if err != nil {
		return nil, 0, false, fmt.Errorf("select bootstrap spans: %w", err)
	}
	agentsByID := make(map[string]*archiveAgentCheckpoint, len(finals))
	for i := range finals {
		agentsByID[finals[i].Checkpoint.AgentID] = &finals[i]
	}
	spanAgents := map[string]string{}
	for _, span := range spans {
		agentID, name, callDigest, identity, err := archiveSpanAgentIdentity(span)
		if err != nil {
			return nil, 0, false, fmt.Errorf("decode agent identity span %s: %w", span.SpanID, err)
		}
		if !identity {
			continue
		}
		agent := agentsByID[agentID]
		if agent == nil {
			return nil, 0, false, fmt.Errorf("agent identity %q has no authoritative final checkpoint", agentID)
		}
		if name != agent.Checkpoint.Name || callDigest != agent.Checkpoint.CallDigest {
			return nil, 0, false, fmt.Errorf("agent %q identity does not match its final checkpoint", agentID)
		}
		agent.IdentitySpanID = span.SpanID
		agent.IdentitySpanIDs = append(agent.IdentitySpanIDs, span.SpanID)
		spanAgents[span.SpanID] = agentID
	}

	allSpanIDs := make(map[string]struct{}, len(spanIDs)+len(finals)+1)
	for spanID := range spanIDs {
		allSpanIDs[spanID] = struct{}{}
	}
	allSpanIDs[manifest.BoundarySpanID] = struct{}{}
	for i := range finals {
		if finals[i].IdentitySpanID == "" {
			finals[i].IdentitySpanID = syntheticAgentSpanID(manifest.Generation, finals[i].Checkpoint.AgentID, allSpanIDs)
			finals[i].SyntheticIdentity = true
			allSpanIDs[finals[i].IdentitySpanID] = struct{}{}
		}
	}
	spansByID := make(map[string]clientdb.Span, len(spans))
	for _, span := range spans {
		spansByID[span.SpanID] = span
	}
	authoritativeParents := map[string]string{}
	for _, final := range finals {
		if final.SyntheticIdentity {
			continue
		}
		wantedAgentID := final.Checkpoint.ParentAgentID
		wantedSpanID := manifest.BoundarySpanID
		if wantedAgentID != "" {
			parent, found := agentsByID[wantedAgentID]
			if !found || parent == nil || parent.IdentitySpanID == "" {
				return nil, 0, false, fmt.Errorf("agent %q references absent authoritative parent %q", final.Checkpoint.AgentID, wantedAgentID)
			}
			wantedSpanID = parent.IdentitySpanID
		}
		for _, spanID := range final.IdentitySpanIDs {
			if archiveProjectedParentAgent(spanID, final.Checkpoint.AgentID, spansByID, spanAgents) != wantedAgentID {
				authoritativeParents[spanID] = wantedSpanID
			}
		}
	}

	controlLogs, err := db.SelectLogsForTraceSpans(ctx, manifest.TraceID, spanIDs, 0)
	if err != nil {
		return nil, 0, false, fmt.Errorf("select agent control logs: %w", err)
	}
	projections := make(map[string]archiveAgentProjection, len(finals))
	logs := make([]clientdb.Log, 0, len(controlLogs)+len(finals)*3)
	logIDs := make([]int64, 0, len(controlLogs)+len(finals))
	seenLogRows := map[int64]struct{}{}
	appendSourceLog := func(row clientdb.Log) {
		if _, seen := seenLogRows[row.ID]; seen {
			return
		}
		seenLogRows[row.ID] = struct{}{}
		logs = append(logs, row)
		logIDs = append(logIDs, row.ID)
	}
	for _, row := range controlLogs {
		if row.ID > cut.Logs {
			continue
		}
		agentID := spanAgents[row.SpanID.String]
		if agentID == "" {
			continue
		}
		projection, control, err := archiveAgentControl(row, projections[agentID])
		if err != nil {
			return nil, 0, false, fmt.Errorf("decode agent control log %d: %w", row.ID, err)
		}
		if !control {
			continue
		}
		projections[agentID] = projection
		appendSourceLog(row)
	}
	for i := range finals {
		appendSourceLog(finals[i].Row)
	}

	roots := make([]string, 0, len(finals))
	for _, final := range finals {
		roots = append(roots, final.Checkpoint.SnapshotDigest)
	}
	payloadLogs, err := selectCallPayloadClosure(ctx, db, manifest.TraceID, cut.Logs, roots)
	if err != nil {
		return nil, 0, false, err
	}
	for _, row := range payloadLogs {
		appendSourceLog(row)
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].ID < logs[j].ID })

	for i := range finals {
		final := &finals[i]
		projection := projections[final.Checkpoint.AgentID]
		needState := final.SyntheticIdentity || !projection.stateMatches(final.Checkpoint)
		needSnapshot := final.SyntheticIdentity || projection.SnapshotDigest != final.Checkpoint.SnapshotDigest
		if needState || needSnapshot {
			synthetic, err := syntheticAgentControlLogs(manifest.TraceID, final.IdentitySpanID, final.Checkpoint, sealAt, needState, needSnapshot)
			if err != nil {
				return nil, 0, false, err
			}
			logs = append(logs, synthetic...)
		}
	}

	signals := make([]archive.BootstrapSignal, 0, (len(spans)+len(logs))/otlpBatchSize+3)
	for start := 0; start < len(spans); start += otlpBatchSize {
		end := min(start+otlpBatchSize, len(spans))
		readOnly := make([]sdktrace.ReadOnlySpan, end-start)
		for i := start; i < end; i++ {
			readOnly[i-start] = spans[i].ReadOnly()
		}
		resourceSpans, err := archiveSpansToPB(readOnly, manifest, sealAt, start == 0, func(spanID string) bool {
			return db.HasSpanForTrace(manifest.TraceID, spanID)
		})
		if err != nil {
			return nil, 0, false, err
		}
		if err := reparentArchiveIdentitySpans(resourceSpans, authoritativeParents); err != nil {
			return nil, 0, false, err
		}
		payload, err := proto.Marshal(&coltracepb.ExportTraceServiceRequest{ResourceSpans: resourceSpans})
		if err != nil {
			return nil, 0, false, err
		}
		recordCount := int64(end - start)
		if start == 0 {
			recordCount++
		}
		signals = append(signals, archive.BootstrapSignal{Kind: archive.BootstrapFrameTraces, Payload: payload, Records: recordCount})
	}
	syntheticSpans, err := syntheticAgentIdentitySpans(manifest, sealAt, finals, len(spans) == 0)
	if err != nil {
		return nil, 0, false, err
	}
	if len(syntheticSpans) > 0 {
		payload, err := proto.Marshal(&coltracepb.ExportTraceServiceRequest{ResourceSpans: syntheticSpans})
		if err != nil {
			return nil, 0, false, err
		}
		recordCount := int64(0)
		for _, resource := range syntheticSpans {
			for _, scope := range resource.ScopeSpans {
				recordCount += int64(len(scope.Spans))
			}
		}
		signals = append(signals, archive.BootstrapSignal{Kind: archive.BootstrapFrameTraces, Payload: payload, Records: recordCount})
	}
	for start := 0; start < len(logs); start += otlpBatchSize {
		end := min(start+otlpBatchSize, len(logs))
		resourceLogs := clientdb.LogsToPB(logs[start:end])
		converted := 0
		for _, resource := range resourceLogs {
			for _, scope := range resource.ScopeLogs {
				converted += len(scope.LogRecords)
			}
		}
		if converted != end-start {
			return nil, 0, false, fmt.Errorf("bootstrap log conversion produced %d records, want %d", converted, end-start)
		}
		payload, err := proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: resourceLogs})
		if err != nil {
			return nil, 0, false, err
		}
		signals = append(signals, archive.BootstrapSignal{Kind: archive.BootstrapFrameLogs, Payload: payload, Records: int64(converted)})
	}
	excludedSpans := make([]string, 0, len(allSpanIDs))
	for spanID := range allSpanIDs {
		excludedSpans = append(excludedSpans, spanID)
	}
	sort.Strings(excludedSpans)
	sort.Slice(logIDs, func(i, j int) bool { return logIDs[i] < logIDs[j] })
	data, records, err := archive.BuildBootstrap(archive.BootstrapHeader{
		Generation: manifest.Generation, TraceID: manifest.TraceID, SealAt: sealAt.Format(time.RFC3339Nano),
		HighWater: archive.HighWater{Spans: cut.Spans, Logs: cut.Logs, Metrics: cut.Metrics},
	}, signals, archive.BootstrapExclusions{SpanIDs: excludedSpans, LogRowIDs: logIDs})
	return data, records, true, err
}

type archiveAgentCheckpoint struct {
	Checkpoint        core.AgentCheckpoint
	Row               clientdb.Log
	IdentitySpanID    string
	IdentitySpanIDs   []string
	SyntheticIdentity bool
}

func archiveProjectedParentAgent(spanID, agentID string, spans map[string]clientdb.Span, spanAgents map[string]string) string {
	seen := map[string]struct{}{spanID: {}}
	span := spans[spanID]
	for span.ParentSpanID.Valid {
		parentID := span.ParentSpanID.String
		if _, cycle := seen[parentID]; cycle {
			return ""
		}
		seen[parentID] = struct{}{}
		if parentAgentID := spanAgents[parentID]; parentAgentID != "" && parentAgentID != agentID {
			return parentAgentID
		}
		parent, found := spans[parentID]
		if !found {
			return ""
		}
		span = parent
	}
	return ""
}

func reparentArchiveIdentitySpans(resources []*otlptracev1.ResourceSpans, parents map[string]string) error {
	for _, resource := range resources {
		for _, scope := range resource.ScopeSpans {
			for _, span := range scope.Spans {
				parentID, repair := parents[hex.EncodeToString(span.SpanId)]
				if !repair {
					continue
				}
				decoded, err := hex.DecodeString(parentID)
				if err != nil || len(decoded) != 8 {
					return fmt.Errorf("invalid authoritative archive parent span ID %q", parentID)
				}
				span.ParentSpanId = decoded
			}
		}
	}
	return nil
}

type archiveAgentProjection struct {
	State            string
	PreTeardownState string
	StopReason       string
	SnapshotDigest   string
}

func (projection archiveAgentProjection) stateMatches(checkpoint core.AgentCheckpoint) bool {
	if projection.State != string(checkpoint.State) || projection.StopReason != string(checkpoint.StopReason) {
		return false
	}
	if checkpoint.State == core.AgentStateStopped && checkpoint.StopReason == core.AgentStopSession {
		return projection.PreTeardownState == string(checkpoint.PreTeardownState)
	}
	return true
}

func selectFinalAgentCheckpoints(ctx context.Context, db *clientdb.DB, traceID string, high int64) ([]archiveAgentCheckpoint, error) {
	bySequence := map[uint64]archiveAgentCheckpoint{}
	for cursor := int64(0); cursor < high; {
		rows, err := db.SelectLogsRange(ctx, clientdb.SelectLogsRangeParams{AfterID: cursor, ThroughID: high, Limit: otlpBatchSize})
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("log stream ended at %d before high-water %d", cursor, high)
		}
		for _, row := range rows {
			cursor = row.ID
			if !row.TraceID.Valid || row.TraceID.String != traceID {
				return nil, fmt.Errorf("checkpoint scan encountered log row %d from trace %q", row.ID, row.TraceID.String)
			}
			checkpoint, reserved, err := decodeAgentCheckpoint(row)
			if err != nil {
				return nil, fmt.Errorf("row %d: %w", row.ID, err)
			}
			if !reserved {
				continue
			}
			if _, duplicate := bySequence[checkpoint.Sequence]; duplicate {
				return nil, fmt.Errorf("checkpoint sequence %d occurs more than once", checkpoint.Sequence)
			}
			bySequence[checkpoint.Sequence] = archiveAgentCheckpoint{Checkpoint: checkpoint, Row: row}
		}
	}
	if len(bySequence) == 0 {
		return nil, nil
	}
	var expected uint64
	finals := make([]archiveAgentCheckpoint, 0)
	checkpointAgents := map[string]struct{}{}
	for _, record := range bySequence {
		checkpoint := record.Checkpoint
		if checkpoint.AgentID == "" {
			return nil, fmt.Errorf("checkpoint %d has no agent identity", checkpoint.Sequence)
		}
		checkpointAgents[checkpoint.AgentID] = struct{}{}
		if !checkpoint.Final {
			if checkpoint.ExpectedFinalSequence != 0 {
				return nil, fmt.Errorf("non-final checkpoint %d declares expected final sequence %d", checkpoint.Sequence, checkpoint.ExpectedFinalSequence)
			}
			continue
		}
		if checkpoint.ExpectedFinalSequence == 0 {
			return nil, fmt.Errorf("final checkpoint %d has no expected final sequence", checkpoint.Sequence)
		}
		if checkpoint.Sequence > checkpoint.ExpectedFinalSequence {
			return nil, fmt.Errorf("final checkpoint %d exceeds expected final sequence %d", checkpoint.Sequence, checkpoint.ExpectedFinalSequence)
		}
		if expected == 0 {
			expected = checkpoint.ExpectedFinalSequence
		} else if checkpoint.ExpectedFinalSequence != expected {
			return nil, fmt.Errorf("final checkpoint %d expects sequence %d, want %d", checkpoint.Sequence, checkpoint.ExpectedFinalSequence, expected)
		}
		if checkpoint.Name == "" || checkpoint.CallDigest == "" || checkpoint.SnapshotDigest == "" {
			return nil, fmt.Errorf("final checkpoint %d is missing resume identity", checkpoint.Sequence)
		}
		finals = append(finals, record)
	}
	if len(finals) == 0 {
		return nil, errors.New("checkpoint stream has no final records")
	}
	if uint64(len(bySequence)) != expected {
		for sequence := uint64(1); sequence <= expected; sequence++ {
			if _, found := bySequence[sequence]; !found {
				return nil, fmt.Errorf("checkpoint sequence %d is missing before expected final sequence %d", sequence, expected)
			}
		}
		return nil, fmt.Errorf("checkpoint stream has records beyond expected final sequence %d", expected)
	}
	for sequence := uint64(1); sequence <= expected; sequence++ {
		if _, found := bySequence[sequence]; !found {
			return nil, fmt.Errorf("checkpoint sequence %d is missing before expected final sequence %d", sequence, expected)
		}
	}
	if uint64(len(finals)) > expected {
		return nil, fmt.Errorf("%d final checkpoints exceed expected final sequence %d", len(finals), expected)
	}
	firstFinal := expected - uint64(len(finals)) + 1
	seenAgents := map[string]struct{}{}
	for _, final := range finals {
		if final.Checkpoint.Sequence < firstFinal || final.Checkpoint.Sequence > expected {
			return nil, fmt.Errorf("final checkpoint sequence %d is outside authoritative suffix %d..%d", final.Checkpoint.Sequence, firstFinal, expected)
		}
		if _, duplicate := seenAgents[final.Checkpoint.AgentID]; duplicate {
			return nil, fmt.Errorf("agent %q has more than one final checkpoint", final.Checkpoint.AgentID)
		}
		seenAgents[final.Checkpoint.AgentID] = struct{}{}
	}
	parentByAgent := make(map[string]string, len(finals))
	for _, final := range finals {
		parentID := final.Checkpoint.ParentAgentID
		parentByAgent[final.Checkpoint.AgentID] = parentID
		if parentID == "" {
			continue
		}
		if parentID == final.Checkpoint.AgentID {
			return nil, fmt.Errorf("agent %q is its own parent", final.Checkpoint.AgentID)
		}
		if _, found := seenAgents[parentID]; !found {
			return nil, fmt.Errorf("agent %q references missing parent agent %q", final.Checkpoint.AgentID, parentID)
		}
	}
	visitState := map[string]uint8{}
	var visitParent func(string) error
	visitParent = func(agentID string) error {
		switch visitState[agentID] {
		case 1:
			return fmt.Errorf("authoritative agent parent cycle includes %q", agentID)
		case 2:
			return nil
		}
		visitState[agentID] = 1
		if parentID := parentByAgent[agentID]; parentID != "" {
			if err := visitParent(parentID); err != nil {
				return err
			}
		}
		visitState[agentID] = 2
		return nil
	}
	for agentID := range parentByAgent {
		if err := visitParent(agentID); err != nil {
			return nil, err
		}
	}
	for agentID := range checkpointAgents {
		if _, found := seenAgents[agentID]; !found {
			return nil, fmt.Errorf("agent %q has no authoritative final checkpoint", agentID)
		}
	}
	for sequence := firstFinal; sequence <= expected; sequence++ {
		if !bySequence[sequence].Checkpoint.Final {
			return nil, fmt.Errorf("checkpoint sequence %d in authoritative final suffix is not final", sequence)
		}
	}
	sort.Slice(finals, func(i, j int) bool { return finals[i].Checkpoint.AgentID < finals[j].Checkpoint.AgentID })
	return finals, nil
}

func decodeAgentCheckpoint(row clientdb.Log) (core.AgentCheckpoint, bool, error) {
	var scope otlpcommonv1.InstrumentationScope
	if len(row.InstrumentationScope) > 0 {
		if err := protojson.Unmarshal(row.InstrumentationScope, &scope); err != nil {
			return core.AgentCheckpoint{}, false, fmt.Errorf("decode instrumentation scope: %w", err)
		}
	}
	attrs, err := archiveLogAttributes(row)
	if err != nil {
		return core.AgentCheckpoint{}, false, err
	}
	_, markerPresent := attrs[telemetryattrs.AgentCheckpointAttr]
	reserved := scope.Name == telemetryattrs.AgentCheckpointInstrumentationScope || markerPresent
	if !reserved {
		return core.AgentCheckpoint{}, false, nil
	}
	marker, markerValid := archiveBoolValue(attrs[telemetryattrs.AgentCheckpointAttr])
	if scope.Name != telemetryattrs.AgentCheckpointInstrumentationScope || !markerValid || !marker {
		return core.AgentCheckpoint{}, true, errors.New("invalid checkpoint scope or marker")
	}
	contract, contractValid := archiveStringValue(attrs[telemetryattrs.AgentCheckpointContractAttr])
	if !contractValid || contract != telemetryattrs.AgentCheckpointContractV1 {
		return core.AgentCheckpoint{}, true, errors.New("unsupported checkpoint contract")
	}
	sequenceValue, sequenceValid := archiveStringValue(attrs[telemetryattrs.AgentCheckpointSequenceAttr])
	sequence, err := strconv.ParseUint(sequenceValue, 10, 64)
	if !sequenceValid || err != nil || sequence == 0 {
		return core.AgentCheckpoint{}, true, errors.New("invalid checkpoint sequence attribute")
	}
	var body otlpcommonv1.AnyValue
	if err := proto.Unmarshal(row.Body, &body); err != nil {
		return core.AgentCheckpoint{}, true, fmt.Errorf("decode checkpoint body: %w", err)
	}
	bodyBytes := body.GetBytesValue()
	if len(bodyBytes) == 0 {
		return core.AgentCheckpoint{}, true, errors.New("checkpoint body must be non-empty bytes")
	}
	var checkpoint core.AgentCheckpoint
	if err := json.Unmarshal(bodyBytes, &checkpoint); err != nil {
		return core.AgentCheckpoint{}, true, fmt.Errorf("decode version-1 checkpoint JSON: %w", err)
	}
	if checkpoint.Version != core.AgentCheckpointVersion {
		return core.AgentCheckpoint{}, true, fmt.Errorf("unsupported checkpoint version %d", checkpoint.Version)
	}
	if checkpoint.Sequence != sequence {
		return core.AgentCheckpoint{}, true, fmt.Errorf("checkpoint body sequence %d does not match attribute %d", checkpoint.Sequence, sequence)
	}
	finalAttr, finalPresent := attrs[telemetryattrs.AgentCheckpointFinalAttr]
	finalValue, finalValid := archiveBoolValue(finalAttr)
	if !finalPresent || !finalValid || finalValue != checkpoint.Final {
		return core.AgentCheckpoint{}, true, errors.New("checkpoint final attribute does not match body")
	}
	return checkpoint, true, nil
}

func archiveLogAttributes(row clientdb.Log) (map[string]*otlpcommonv1.AnyValue, error) {
	var attrs []*otlpcommonv1.KeyValue
	if err := clientdb.UnmarshalProtoJSONs(row.Attributes, &otlpcommonv1.KeyValue{}, &attrs); err != nil {
		return nil, fmt.Errorf("decode log attributes: %w", err)
	}
	values := make(map[string]*otlpcommonv1.AnyValue, len(attrs))
	for _, attr := range attrs {
		values[attr.GetKey()] = attr.GetValue()
	}
	return values, nil
}

func archiveStringValue(value *otlpcommonv1.AnyValue) (string, bool) {
	if value == nil {
		return "", false
	}
	stringValue, ok := value.GetValue().(*otlpcommonv1.AnyValue_StringValue)
	if !ok {
		return "", false
	}
	return stringValue.StringValue, true
}

func archiveBoolValue(value *otlpcommonv1.AnyValue) (bool, bool) {
	if value == nil {
		return false, false
	}
	boolValue, ok := value.GetValue().(*otlpcommonv1.AnyValue_BoolValue)
	if !ok {
		return false, false
	}
	return boolValue.BoolValue, true
}

func archiveSpanAgentIdentity(span clientdb.Span) (agentID, name, callDigest string, identity bool, err error) {
	var attrs []*otlpcommonv1.KeyValue
	if err := clientdb.UnmarshalProtoJSONs(span.Attributes, &otlpcommonv1.KeyValue{}, &attrs); err != nil {
		return "", "", "", false, err
	}
	var marker bool
	for _, attr := range attrs {
		switch attr.GetKey() {
		case telemetryattrs.AgentAttr:
			marker = attr.GetValue().GetBoolValue()
		case telemetryattrs.AgentIDAttr:
			agentID = attr.GetValue().GetStringValue()
		case telemetryattrs.AgentNameAttr:
			name = attr.GetValue().GetStringValue()
		case telemetryattrs.AgentCallDigestAttr:
			callDigest = attr.GetValue().GetStringValue()
		}
	}
	return agentID, name, callDigest, marker && agentID != "", nil
}

func archiveAgentControl(row clientdb.Log, projection archiveAgentProjection) (archiveAgentProjection, bool, error) {
	attrs, err := archiveLogAttributes(row)
	if err != nil {
		return projection, false, err
	}
	stateValue, state := attrs[telemetryattrs.AgentStateAttr]
	snapshotValue, snapshot := attrs[telemetryattrs.AgentSnapshotDigestAttr]
	if !state && !snapshot {
		return projection, false, nil
	}
	if state {
		stateString, valid := archiveStringValue(stateValue)
		if !valid {
			return projection, false, errors.New("agent state must be a string")
		}
		projection.State = stateString
		if stopValue, present := attrs[telemetryattrs.AgentStopReasonAttr]; present {
			stopReason, valid := archiveStringValue(stopValue)
			if !valid {
				return projection, false, errors.New("agent stop reason must be a string")
			}
			projection.StopReason = stopReason
		} else {
			projection.StopReason = ""
		}
		if projection.State != string(core.AgentStateStopped) || projection.StopReason != string(core.AgentStopSession) {
			projection.PreTeardownState = projection.State
		}
	}
	if snapshot {
		snapshotDigest, valid := archiveStringValue(snapshotValue)
		if !valid || snapshotDigest == "" {
			return projection, false, errors.New("agent snapshot digest must be a non-empty string")
		}
		projection.SnapshotDigest = snapshotDigest
	}
	return projection, true, nil
}

func selectCallPayloadClosure(ctx context.Context, db *clientdb.DB, traceID string, high int64, roots []string) ([]clientdb.Log, error) {
	queue := append([]string(nil), roots...)
	seen := map[string]struct{}{}
	rows := make([]clientdb.Log, 0)
	for len(queue) > 0 {
		digest := queue[0]
		queue = queue[1:]
		if digest == "" {
			return nil, errors.New("call-payload closure has an empty root digest")
		}
		if _, found := seen[digest]; found {
			continue
		}
		seen[digest] = struct{}{}
		row, err := db.SelectCallPayload(ctx, traceID, digest)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("call payload %s is missing", digest)
			}
			return nil, fmt.Errorf("select call payload %s: %w", digest, err)
		}
		if row.ID > high {
			return nil, fmt.Errorf("call payload %s is outside fixed log cut %d", digest, high)
		}
		callPB, decodedDigest, err := decodeCallPayloadLog(row)
		if err != nil {
			return nil, fmt.Errorf("decode call payload %s: %w", digest, err)
		}
		if decodedDigest != digest {
			return nil, fmt.Errorf("call payload indexed as %s decodes as %s", digest, decodedDigest)
		}
		rows = append(rows, row)
		queue = append(queue, referencedCallDigests(callPB)...)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func decodeCallPayloadLog(row clientdb.Log) (*callpbv1.Call, string, error) {
	var body otlpcommonv1.AnyValue
	if err := proto.Unmarshal(row.Body, &body); err != nil {
		return nil, "", err
	}
	callPB := new(callpbv1.Call)
	if err := proto.Unmarshal(body.GetBytesValue(), callPB); err != nil {
		return nil, "", err
	}
	if callPB.GetDigest() == "" {
		return nil, "", fmt.Errorf("call payload missing embedded digest")
	}
	return callPB, callPB.GetDigest(), nil
}

func referencedCallDigests(callPB *callpbv1.Call) []string {
	refs := make([]string, 0, 2+len(callPB.GetArgs())+len(callPB.GetImplicitInputs()))
	if callPB.GetReceiverDigest() != "" {
		refs = append(refs, callPB.GetReceiverDigest())
	}
	if callPB.GetModule().GetCallDigest() != "" {
		refs = append(refs, callPB.GetModule().GetCallDigest())
	}
	var walkLiteral func(*callpbv1.Literal)
	walkLiteral = func(literal *callpbv1.Literal) {
		switch value := literal.GetValue().(type) {
		case *callpbv1.Literal_CallDigest:
			refs = append(refs, value.CallDigest)
		case *callpbv1.Literal_List:
			for _, item := range value.List.GetValues() {
				walkLiteral(item)
			}
		case *callpbv1.Literal_Object:
			for _, field := range value.Object.GetValues() {
				walkLiteral(field.GetValue())
			}
		}
	}
	for _, arg := range append(append([]*callpbv1.Argument(nil), callPB.GetArgs()...), callPB.GetImplicitInputs()...) {
		walkLiteral(arg.GetValue())
	}
	return refs
}

func syntheticAgentSpanID(generation, agentID string, occupied map[string]struct{}) string {
	for salt := 0; ; salt++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", generation, agentID, salt)))
		spanID := hex.EncodeToString(sum[:8])
		if spanID == "0000000000000000" {
			continue
		}
		if _, found := occupied[spanID]; !found {
			return spanID
		}
	}
}

func syntheticAgentIdentitySpans(manifest archive.Manifest, sealAt time.Time, agents []archiveAgentCheckpoint, includeBoundary bool) ([]*otlptracev1.ResourceSpans, error) {
	traceID, err := hex.DecodeString(manifest.TraceID)
	if err != nil || len(traceID) != 16 {
		return nil, fmt.Errorf("invalid archive trace ID %q", manifest.TraceID)
	}
	boundaryID, err := hex.DecodeString(manifest.BoundarySpanID)
	if err != nil || len(boundaryID) != 8 {
		return nil, fmt.Errorf("invalid archive boundary span ID %q", manifest.BoundarySpanID)
	}
	spanByAgent := make(map[string]string, len(agents))
	for _, agent := range agents {
		spanByAgent[agent.Checkpoint.AgentID] = agent.IdentitySpanID
	}
	spans := make([]*otlptracev1.Span, 0, len(agents)+1)
	if includeBoundary {
		spans = append(spans, archiveBoundarySpan(manifest, sealAt, traceID, boundaryID))
	}
	for _, agent := range agents {
		if !agent.SyntheticIdentity {
			continue
		}
		spanID, _ := hex.DecodeString(agent.IdentitySpanID)
		parentID := boundaryID
		if parentSpan := spanByAgent[agent.Checkpoint.ParentAgentID]; parentSpan != "" {
			parentID, _ = hex.DecodeString(parentSpan)
		}
		status := &otlptracev1.Status{}
		if agent.Checkpoint.Error != "" {
			status.Code = otlptracev1.Status_STATUS_CODE_ERROR
			status.Message = agent.Checkpoint.Error
		}
		spans = append(spans, &otlptracev1.Span{
			TraceId: traceID, SpanId: spanID, ParentSpanId: parentID,
			Name: agent.Checkpoint.Name, Kind: otlptracev1.Span_SPAN_KIND_INTERNAL,
			StartTimeUnixNano: uint64(sealAt.UnixNano()), EndTimeUnixNano: uint64(sealAt.UnixNano()), Status: status,
			Attributes: telemetry.KeyValues([]attribute.KeyValue{
				attribute.Bool(telemetryattrs.AgentAttr, true),
				attribute.String(telemetryattrs.AgentIDAttr, agent.Checkpoint.AgentID),
				attribute.String(telemetryattrs.AgentNameAttr, agent.Checkpoint.Name),
				attribute.String(telemetryattrs.AgentCallDigestAttr, agent.Checkpoint.CallDigest),
			}),
		})
	}
	if len(spans) == 0 {
		return nil, nil
	}
	return []*otlptracev1.ResourceSpans{{
		Resource: &otlpresourcev1.Resource{},
		ScopeSpans: []*otlptracev1.ScopeSpans{{
			Scope: &otlpcommonv1.InstrumentationScope{Name: core.AgentInstrumentationScope}, Spans: spans,
		}},
	}}, nil
}

func archiveBoundarySpan(manifest archive.Manifest, sealAt time.Time, traceID, boundaryID []byte) *otlptracev1.Span {
	start := manifest.StartedAt
	if start.IsZero() {
		start = sealAt
	}
	return &otlptracev1.Span{
		TraceId: traceID, SpanId: boundaryID, Name: "engine archive", Kind: otlptracev1.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(sealAt.UnixNano()),
		Attributes: telemetry.KeyValues([]attribute.KeyValue{attribute.Bool(telemetry.UIPassthroughAttr, true)}),
	}
}

func syntheticAgentControlLogs(traceID, spanID string, checkpoint core.AgentCheckpoint, at time.Time, includeState, includeSnapshot bool) ([]clientdb.Log, error) {
	resource, err := protojson.Marshal(&otlpresourcev1.Resource{})
	if err != nil {
		return nil, err
	}
	scope, err := protojson.Marshal(&otlpcommonv1.InstrumentationScope{Name: core.AgentInstrumentationScope})
	if err != nil {
		return nil, err
	}
	body, err := proto.Marshal(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: ""}})
	if err != nil {
		return nil, err
	}
	makeLog := func(attrs []*otlpcommonv1.KeyValue) (clientdb.Log, error) {
		encoded, err := clientdb.MarshalProtoJSONs(attrs)
		if err != nil {
			return clientdb.Log{}, err
		}
		return clientdb.Log{
			TraceID: sql.NullString{String: traceID, Valid: true}, SpanID: sql.NullString{String: spanID, Valid: true},
			Timestamp: at.UnixNano(), Body: body, Attributes: encoded, InstrumentationScope: scope, Resource: resource,
		}, nil
	}
	stringAttr := func(key, value string) *otlpcommonv1.KeyValue {
		return &otlpcommonv1.KeyValue{Key: key, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: value}}}
	}
	logs := make([]clientdb.Log, 0, 3)
	if includeState && checkpoint.State == core.AgentStateStopped && checkpoint.StopReason == core.AgentStopSession {
		row, err := makeLog([]*otlpcommonv1.KeyValue{
			stringAttr(telemetryattrs.AgentStateAttr, string(checkpoint.PreTeardownState)),
			stringAttr(telemetryattrs.AgentWaitingOnAttr, ""), stringAttr(telemetryattrs.AgentStopReasonAttr, ""),
		})
		if err != nil {
			return nil, err
		}
		logs = append(logs, row)
	}
	if includeState {
		state, err := makeLog([]*otlpcommonv1.KeyValue{
			stringAttr(telemetryattrs.AgentStateAttr, string(checkpoint.State)),
			stringAttr(telemetryattrs.AgentWaitingOnAttr, ""), stringAttr(telemetryattrs.AgentStopReasonAttr, string(checkpoint.StopReason)),
		})
		if err != nil {
			return nil, err
		}
		logs = append(logs, state)
	}
	if includeSnapshot {
		snapshot, err := makeLog([]*otlpcommonv1.KeyValue{stringAttr(telemetryattrs.AgentSnapshotDigestAttr, checkpoint.SnapshotDigest)})
		if err != nil {
			return nil, err
		}
		logs = append(logs, snapshot)
	}
	return logs, nil
}

func stringSliceSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func archiveSpansToPB(spans []sdktrace.ReadOnlySpan, manifest archive.Manifest, sealAt time.Time, includeBoundary bool, hasSpan func(string) bool) ([]*otlptracev1.ResourceSpans, error) {
	resourceSpans := telemetry.SpansToPB(spans)
	boundaryID, err := hex.DecodeString(manifest.BoundarySpanID)
	if err != nil || len(boundaryID) != 8 {
		return nil, fmt.Errorf("invalid archive boundary span ID %q", manifest.BoundarySpanID)
	}
	for _, resource := range resourceSpans {
		for _, scope := range resource.ScopeSpans {
			for _, span := range scope.Spans {
				if len(span.ParentSpanId) > 0 && !hasSpan(hex.EncodeToString(span.ParentSpanId)) {
					span.ParentSpanId = append([]byte(nil), boundaryID...)
				}
			}
		}
	}
	if !includeBoundary {
		return resourceSpans, nil
	}
	if len(resourceSpans) == 0 || len(resourceSpans[0].ScopeSpans) == 0 {
		return nil, errors.New("cannot synthesize archive boundary without a resource span")
	}
	traceID, err := hex.DecodeString(manifest.TraceID)
	if err != nil || len(traceID) != 16 {
		return nil, fmt.Errorf("invalid archive trace ID %q", manifest.TraceID)
	}
	resourceSpans[0].ScopeSpans[0].Spans = append(resourceSpans[0].ScopeSpans[0].Spans,
		archiveBoundarySpan(manifest, sealAt, traceID, boundaryID))
	return resourceSpans, nil
}

func (srv *Server) archiveRequestRecord(metadataClientID, sessionID, token string) (*clientRecord, error) {
	record, err := srv.clientRecordFromIDs(sessionID, metadataClientID)
	if err != nil {
		return nil, err
	}
	if record.clientID != record.daggerSession.mainClientCallerID {
		return nil, errors.New("archive APIs are available only to the main client")
	}
	if subtle.ConstantTimeCompare([]byte(record.secretToken), []byte(token)) != 1 {
		return nil, errors.New("invalid client secret token")
	}
	return record, nil
}

func (srv *Server) serveArchiveHTTP(w http.ResponseWriter, r *http.Request, record *clientRecord) error {
	const prefix = "/v1/telemetry/archives"
	path := strings.TrimPrefix(r.URL.Path, prefix)
	if path == "" || path == "/" {
		if r.Method != http.MethodGet {
			return httpErr(errors.New("method not allowed"), http.StatusMethodNotAllowed)
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		excludeTraceID := ""
		if record.clientMetadata != nil {
			excludeTraceID = record.clientMetadata.ArchiveTraceID
		}
		page := srv.archives.List(r.URL.Query().Get("after"), excludeTraceID, limit)
		return writeArchiveJSON(w, http.StatusOK, page)
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		return httpErr(errors.New("archive endpoint not found"), http.StatusNotFound)
	}
	traceID, resource := parts[0], parts[1]
	if resource == "metadata" {
		if r.Method != http.MethodPost {
			return httpErr(errors.New("method not allowed"), http.StatusMethodNotAllowed)
		}
		manifest, err := srv.archives.Manifest(traceID)
		if err != nil {
			return writeArchiveFailure(w, err)
		}
		if manifest.MainClientID != record.clientID {
			return httpErr(errors.New("only the archive owner may update active metadata"), http.StatusForbidden)
		}
		var update struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&update); err != nil {
			return httpErr(fmt.Errorf("decode metadata: %w", err), http.StatusBadRequest)
		}
		if err := srv.archives.UpdateTitle(traceID, manifest.Generation, update.Title); err != nil {
			return writeArchiveFailure(w, err)
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	if r.Method != http.MethodGet {
		return httpErr(errors.New("method not allowed"), http.StatusMethodNotAllowed)
	}
	lease, err := srv.archives.Acquire(traceID)
	if err != nil {
		return writeArchiveFailure(w, err)
	}
	defer lease.Release()
	manifest := lease.Manifest()
	if generation := r.Header.Get("X-Dagger-Archive-Generation"); generation != "" && generation != manifest.Generation {
		return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureCorrupt, Err: errors.New("archive generation mismatch")})
	}
	w.Header().Set("X-Dagger-Archive-Generation", manifest.Generation)
	switch resource {
	case "bootstrap":
		return serveArchiveBootstrap(w, lease)
	case "traces", "logs", "metrics":
		return srv.serveArchiveSignal(w, r, manifest, resource)
	default:
		return httpErr(errors.New("archive endpoint not found"), http.StatusNotFound)
	}
}

func serveArchiveBootstrap(w http.ResponseWriter, lease *archive.Lease) error {
	file, err := os.Open(lease.BootstrapPath())
	if err != nil {
		return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureIO, Err: err})
	}
	defer file.Close()
	hash := sha256.New()
	if _, _, err := archive.VerifyBootstrap(io.TeeReader(file, hash)); err != nil {
		return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureCorrupt, Err: err})
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != lease.Manifest().Bootstrap.SHA256 {
		return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureCorrupt, Err: fmt.Errorf("bootstrap sidecar checksum is %s, want %s", got, lease.Manifest().Bootstrap.SHA256)})
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	w.Header().Set("Content-Type", archive.BootstrapContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, file)
	return err
}

func (srv *Server) serveArchiveSignal(w http.ResponseWriter, r *http.Request, manifest archive.Manifest, signal string) error {
	cursor, err := archiveCursor(r)
	if err != nil {
		return httpErr(err, http.StatusBadRequest)
	}
	db, err := srv.clientDBs.Open(r.Context(), manifest.MainClientID)
	if err != nil {
		return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureIO, Err: err})
	}
	defer db.Close()

	excludedSpans := stringSet(r.URL.Query()["exclude_span"])
	excludedLogs := int64Set(r.URL.Query()["exclude_log"])
	var high int64
	switch signal {
	case "traces":
		high = manifest.HighWater.Spans
	case "logs":
		high = manifest.HighWater.Logs
	case "metrics":
		high = manifest.HighWater.Metrics
	}
	if cursor > high {
		return httpErr(fmt.Errorf("archive cursor %d exceeds fixed high-water %d", cursor, high), http.StatusBadRequest)
	}
	w.Header().Set("Content-Type", enginetel.LiveContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	batchLimit := otlpBatchSize
	for cursor < high {
		scan := cursor
		scanned := 0
		var message proto.Message
		switch signal {
		case "traces":
			rows, err := db.SelectSpansRange(r.Context(), clientdb.SelectSpansRangeParams{
				AfterID: cursor, ThroughID: high, Limit: int64(batchLimit),
			})
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				cursor = high
				continue
			}
			scanned = len(rows)
			scan = rows[len(rows)-1].ID
			ro := make([]sdktrace.ReadOnlySpan, 0, len(rows))
			for _, row := range rows {
				if row.TraceID == manifest.TraceID {
					if _, excluded := excludedSpans[row.SpanID]; !excluded {
						ro = append(ro, row.ReadOnly())
					}
				}
			}
			if len(ro) > 0 {
				sealAt := time.Time{}
				if manifest.SealAt != nil {
					sealAt = *manifest.SealAt
				}
				resourceSpans, err := archiveSpansToPB(ro, manifest, sealAt, false, func(spanID string) bool {
					return db.HasSpanForTrace(manifest.TraceID, spanID)
				})
				if err != nil {
					return err
				}
				message = &coltracepb.ExportTraceServiceRequest{ResourceSpans: resourceSpans}
			}
		case "logs":
			rows, err := db.SelectLogsRange(r.Context(), clientdb.SelectLogsRangeParams{
				AfterID: cursor, ThroughID: high, Limit: int64(batchLimit),
			})
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				cursor = high
				continue
			}
			scanned = len(rows)
			scan = rows[len(rows)-1].ID
			filtered := rows[:0]
			for _, row := range rows {
				_, excluded := excludedLogs[row.ID]
				if !excluded && (!row.TraceID.Valid || row.TraceID.String == manifest.TraceID) {
					filtered = append(filtered, row)
				}
			}
			if len(filtered) > 0 {
				message = &collogspb.ExportLogsServiceRequest{ResourceLogs: clientdb.LogsToPB(filtered)}
			}
		case "metrics":
			rows, err := db.SelectMetricsRange(r.Context(), clientdb.SelectMetricsRangeParams{
				AfterID: cursor, ThroughID: high, Limit: int64(batchLimit),
			})
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				cursor = high
				continue
			}
			scanned = len(rows)
			scan = rows[len(rows)-1].ID
			message = &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: clientdb.MetricsToPB(rows)}
		}
		if message == nil {
			cursor = scan
			batchLimit = otlpBatchSize
			continue
		}
		payload, err := proto.Marshal(message)
		if err != nil {
			return err
		}
		if len(payload) > enginetel.MaxLivePayloadSize {
			if scanned <= 1 {
				return fmt.Errorf("archive row at cursor %d exceeds maximum payload", scan)
			}
			batchLimit = max(1, scanned/2)
			continue
		}
		cursor = scan
		batchLimit = otlpBatchSize
		if err := enginetel.WriteLiveFrame(w, cursor, payload); err != nil {
			return err
		}
	}
	return enginetel.WriteLiveTerminal(w, high)
}

func archiveCursor(r *http.Request) (int64, error) {
	value := r.Header.Get(enginetel.LiveCursorHeader)
	if value == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 0 {
		return 0, fmt.Errorf("invalid archive cursor %q", value)
	}
	return cursor, nil
}
func stringSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
func int64Set(values []string) map[int64]struct{} {
	out := map[int64]struct{}{}
	for _, value := range values {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			out[parsed] = struct{}{}
		}
	}
	return out
}

func writeArchiveJSON(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(value)
}

func writeArchiveFailure(w http.ResponseWriter, err error) error {
	var failure *archive.Failure
	if !errors.As(err, &failure) {
		failure = &archive.Failure{Kind: archive.FailureIO, Err: err}
	}
	status := http.StatusInternalServerError
	switch failure.Kind {
	case archive.FailureNotFound:
		status = http.StatusNotFound
	case archive.FailureEvicted:
		status = http.StatusGone
	case archive.FailureState:
		status = http.StatusConflict
	case archive.FailureCorrupt:
		status = http.StatusUnprocessableEntity
	}
	return writeArchiveJSON(w, status, map[string]any{"error": failure.Kind, "state": failure.State, "message": failure.Error()})
}
