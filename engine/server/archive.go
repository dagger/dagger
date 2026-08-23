package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
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
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

var (
	agentStateAttrMarker    = []byte(`"` + telemetryattrs.AgentStateAttr + `"`)
	agentSnapshotAttrMarker = []byte(`"` + telemetryattrs.AgentSnapshotDigestAttr + `"`)
	callPayloadScopeMarker  = []byte(telemetryattrs.CallPayloadInstrumentationScope)
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
	cut, err := db.Checkpoint()
	if err != nil {
		return fmt.Errorf("checkpoint telemetry store: %w", err)
	}
	if err := db.ValidateTraceCut(ctx, manifest.TraceID, cut); err != nil {
		return fmt.Errorf("validate archive trace: %w", err)
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
		SealAt:    sealAt, StoreSizeBytes: cut.SizeBytes,
		BootstrapBytes: bootstrapBytes, BootstrapRecords: records,
	})
	return err
}

func buildArchiveBootstrap(ctx context.Context, db *clientdb.DB, manifest archive.Manifest, cut clientdb.Checkpoint, sealAt time.Time) ([]byte, int64, bool, error) {
	identityIDs := db.AgentSpanIDs(manifest.TraceID)
	allSpanIDs := map[string]struct{}{}
	for cursor := int64(0); cursor < cut.Spans; {
		rows, err := db.SelectSpansRange(ctx, cursor, cut.Spans, otlpBatchSize)
		if err != nil {
			return nil, 0, false, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			cursor = row.ID
			allSpanIDs[row.SpanID] = struct{}{}
		}
	}
	if len(identityIDs) == 0 {
		return nil, 0, false, nil
	}
	spanIDs := db.AncestorClosure(identityIDs)
	spans, err := db.SelectSpansLatest(ctx, spanIDs)
	if err != nil {
		return nil, 0, false, fmt.Errorf("select bootstrap spans: %w", err)
	}

	logIDs := make([]int64, 0)
	logs := make([]clientdb.Log, 0)
	for cursor := int64(0); cursor < cut.Logs; {
		rows, err := db.SelectLogsRange(ctx, cursor, cut.Logs, otlpBatchSize)
		if err != nil {
			return nil, 0, false, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			cursor = row.ID
			_, identityLog := spanIDs[row.SpanID.String]
			control := identityLog && (bytes.Contains(row.Attributes, agentStateAttrMarker) || bytes.Contains(row.Attributes, agentSnapshotAttrMarker))
			payload := bytes.Contains(row.InstrumentationScope, callPayloadScopeMarker)
			if control || payload {
				logs = append(logs, row)
				logIDs = append(logIDs, row.ID)
			}
		}
	}

	signals := make([]archive.BootstrapSignal, 0, (len(spans)+len(logs))/otlpBatchSize+2)
	for start := 0; start < len(spans); start += otlpBatchSize {
		end := min(start+otlpBatchSize, len(spans))
		readOnly := make([]sdktrace.ReadOnlySpan, end-start)
		for i := start; i < end; i++ {
			readOnly[i-start] = spans[i].ReadOnly()
		}
		resourceSpans, err := archiveSpansToPB(readOnly, manifest, sealAt, start == 0, func(spanID string) bool {
			_, ok := allSpanIDs[spanID]
			return ok
		})
		if err != nil {
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
	for start := 0; start < len(logs); start += otlpBatchSize {
		end := min(start+otlpBatchSize, len(logs))
		payload, err := proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: clientdb.LogsToPB(logs[start:end])})
		if err != nil {
			return nil, 0, false, err
		}
		signals = append(signals, archive.BootstrapSignal{Kind: archive.BootstrapFrameLogs, Payload: payload, Records: int64(end - start)})
	}
	excludedSpans := make([]string, 0, len(spanIDs))
	for spanID := range spanIDs {
		excludedSpans = append(excludedSpans, spanID)
	}
	excludedSpans = append(excludedSpans, manifest.BoundarySpanID)
	sort.Strings(excludedSpans)
	data, records, err := archive.BuildBootstrap(archive.BootstrapHeader{
		Generation: manifest.Generation, TraceID: manifest.TraceID, SealAt: sealAt.Format(time.RFC3339Nano),
		HighWater: archive.HighWater{Spans: cut.Spans, Logs: cut.Logs, Metrics: cut.Metrics},
	}, signals, archive.BootstrapExclusions{SpanIDs: excludedSpans, LogRowIDs: logIDs})
	return data, records, true, err
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
	start := manifest.StartedAt
	if start.IsZero() {
		start = sealAt
	}
	resourceSpans[0].ScopeSpans[0].Spans = append(resourceSpans[0].ScopeSpans[0].Spans, &otlptracev1.Span{
		TraceId: traceID, SpanId: boundaryID, Name: "engine archive", Kind: otlptracev1.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(sealAt.UnixNano()),
		Attributes: telemetry.KeyValues([]attribute.KeyValue{attribute.Bool(telemetry.UIPassthroughAttr, true)}),
	})
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
			rows, err := db.SelectSpansRange(r.Context(), cursor, high, batchLimit)
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
				resourceSpans, err := archiveSpansToPB(ro, manifest, sealAt, false, db.HasSpan)
				if err != nil {
					return err
				}
				message = &coltracepb.ExportTraceServiceRequest{ResourceSpans: resourceSpans}
			}
		case "logs":
			rows, err := db.SelectLogsRange(r.Context(), cursor, high, batchLimit)
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
			rows, err := db.SelectMetricsRange(r.Context(), cursor, high, batchLimit)
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
