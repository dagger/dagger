package core

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/cloud"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func (m *MCP) loadTraceTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct{ Trace string }) (any, error) {
		id, err := normalizeTraceArg(args.Trace)
		if err != nil {
			return nil, err
		}
		query, err := CurrentQuery(ctx)
		if err != nil {
			return nil, err
		}
		meta, err := query.MainClientCallerMetadata(ctx)
		if err != nil {
			return nil, err
		}
		// Credentials belong to the connecting client, never the engine's ambient
		// environment, a tool argument, or the model's conversation.
		if meta.CloudAuth == nil || meta.CloudAuth.Token == nil {
			return nil, fmt.Errorf("LoadTrace needs Dagger Cloud authentication; run 'dagger login' or set DAGGER_CLOUD_TOKEN before starting the session")
		}
		client, err := cloud.NewOTLPClient(ctx, meta.CloudAuth)
		if err != nil {
			return nil, fmt.Errorf("cannot initialize Dagger Cloud trace client; check the session's Cloud authentication")
		}
		store, err := traceReportClientDB(ctx)
		if err != nil {
			return nil, err
		}
		defer store.Close()
		return loadCloudTrace(ctx, store, id, client.FetchTrace)
	})
}

// URLs are identifiers only: requests always go to the configured Cloud API,
// never to a model-supplied host. Do not accept arbitrary URLs or shell syntax.
func normalizeTraceArg(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if fields := strings.Fields(arg); len(fields) == 3 && fields[0] == "dagger" && fields[1] == "trace" {
		arg = fields[2]
	}
	if u, err := url.Parse(arg); err == nil && u.Scheme == "https" && u.Host == "dagger.cloud" && u.User == nil {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		// Cloud links are /<org>/traces/<id> (and may include a span suffix).
		for i := range parts {
			if parts[i] == "traces" && i+1 < len(parts) {
				arg = parts[i+1]
				break
			}
		}
	}
	id, err := trace.TraceIDFromHex(strings.ToLower(arg))
	if err != nil {
		return "", fmt.Errorf("invalid trace: pass a 32-character hex trace ID, 'dagger trace <id>', or a https://dagger.cloud/<org>/traces/<id> URL")
	}
	return id.String(), nil
}

type cloudTraceFetch func(context.Context, string, cloud.TraceImportSink) error

func loadCloudTrace(ctx context.Context, store *clientdb.DB, id string, fetch cloudTraceFetch) (string, error) {
	imported, err := store.ImportTrace(ctx, id, func(dst *clientdb.DB) error {
		sink := &inspectionTraceSink{db: dst, traceID: id}
		sink.spans = enginetel.NewTraceImporter(enginetel.TraceImportSinks{Spans: inspectionSpanExporter{dst}})
		// Inspection zooms the imported root, rather than embedding it under
		// live work. Preserve its children instead of promoting only reveals.
		sink.spans.KeepRoots = true
		if err := fetch(ctx, id, sink); err != nil {
			// Cloud errors may contain response bodies or credential-refresh details.
			// Keep those out of model-visible errors, including nested tool reports.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("could not load trace %s from Dagger Cloud; check authentication, trace access and network connectivity; no data was imported", id)
		}
		if len(dst.SpanIDs()) == 0 {
			return fmt.Errorf("trace %s contains no spans; no data was imported", id)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	rows, err := imported.SelectSpansLatest(ctx, imported.SpanIDs())
	if err != nil {
		return "", err
	}
	var roots []string
	for _, row := range rows {
		if !row.ParentSpanID.Valid || !imported.HasSpan(row.ParentSpanID.String) {
			roots = append(roots, fmt.Sprintf("%s  %s", row.SpanID, row.Name))
		}
	}
	sort.Strings(roots)
	return fmt.Sprintf("Loaded trace %s (%d spans) for inspection in this session. Historical spans are not live work; no recipes or agents were executed or restored. Repeated loads reuse this snapshot.\nRoots:\n%s\nUse ReadTrace(span: <root>, view: \"report\" | \"inspect\" | \"timings\"), ReadLogs, FindSpans, FindCalls, or InspectCall. Searches include live and loaded traces.", id, len(rows), strings.Join(roots, "\n")), nil
}

// Only inspection readers see this sink's store; nothing is sent through live
// session exporters or back to Cloud. The shared importer seals unfinished
// spans. Preserve logs directly as protobuf, especially binary call payloads.
type inspectionTraceSink struct {
	db      *clientdb.DB
	traceID string
	spans   *enginetel.TraceImporter
}

func (s *inspectionTraceSink) ImportSpans(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) error {
	for _, resource := range req.GetResourceSpans() {
		for _, scope := range resource.GetScopeSpans() {
			for _, span := range scope.GetSpans() {
				if hex.EncodeToString(span.GetTraceId()) != s.traceID {
					return fmt.Errorf("unexpected trace ID in Cloud spans")
				}
			}
		}
	}
	return s.spans.ImportSpans(ctx, req)
}

func (s *inspectionTraceSink) ImportLogs(_ context.Context, req *collogspb.ExportLogsServiceRequest) error {
	for _, resource := range req.GetResourceLogs() {
		res, err := protojson.Marshal(resource.GetResource())
		if err != nil {
			return err
		}
		for _, scope := range resource.GetScopeLogs() {
			instr, err := protojson.Marshal(scope.GetScope())
			if err != nil {
				return err
			}
			rows := make([]clientdb.Log, 0, len(scope.GetLogRecords()))
			for _, rec := range scope.GetLogRecords() {
				tid := hex.EncodeToString(rec.GetTraceId())
				if tid != "" && tid != s.traceID {
					return fmt.Errorf("unexpected trace ID in Cloud logs")
				}
				attrs, err := clientdb.MarshalProtoJSONs(rec.GetAttributes())
				if err != nil {
					return err
				}
				body, err := proto.Marshal(rec.GetBody())
				if err != nil {
					return err
				}
				rows = append(rows, clientdb.Log{
					TraceID:              sql.NullString{String: tid, Valid: tid != ""},
					SpanID:               sql.NullString{String: hex.EncodeToString(rec.GetSpanId()), Valid: len(rec.GetSpanId()) > 0},
					Timestamp:            int64(rec.GetTimeUnixNano()),
					SeverityNumber:       int64(rec.GetSeverityNumber()),
					SeverityText:         rec.GetSeverityText(),
					Body:                 body,
					Attributes:           attrs,
					Resource:             res,
					ResourceSchemaURL:    resource.GetSchemaUrl(),
					InstrumentationScope: instr,
				})
			}
			if _, err := s.db.AppendLogs(rows); err != nil {
				return err
			}
		}
	}
	return nil
}

// The inspection tools do not consume metrics; still drain the stream so a
// fetch has the same completion/error semantics as other Cloud trace readers.
func (s *inspectionTraceSink) ImportMetrics(context.Context, *colmetricspb.ExportMetricsServiceRequest) error {
	return nil
}
func (s *inspectionTraceSink) Seal(ctx context.Context) error { return s.spans.Seal(ctx) }

type inspectionSpanExporter struct{ db *clientdb.DB }

func (e inspectionSpanExporter) Shutdown(context.Context) error { return nil }
func (e inspectionSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, resource := range telemetry.SpansToPB(spans) {
		res, err := protojson.Marshal(resource.GetResource())
		if err != nil {
			return err
		}
		for _, scope := range resource.GetScopeSpans() {
			instr, err := protojson.Marshal(scope.GetScope())
			if err != nil {
				return err
			}
			rows := make([]clientdb.Span, 0, len(scope.GetSpans()))
			for _, span := range scope.GetSpans() {
				attrs, err := clientdb.MarshalProtoJSONs(span.GetAttributes())
				if err != nil {
					return err
				}
				events, err := clientdb.MarshalProtoJSONs(span.GetEvents())
				if err != nil {
					return err
				}
				links, err := clientdb.MarshalProtoJSONs(span.GetLinks())
				if err != nil {
					return err
				}
				status := codes.Unset
				switch span.GetStatus().GetCode() {
				case tracepb.Status_STATUS_CODE_OK:
					status = codes.Ok
				case tracepb.Status_STATUS_CODE_ERROR:
					status = codes.Error
				}
				rows = append(rows, clientdb.Span{
					TraceID:                hex.EncodeToString(span.GetTraceId()),
					SpanID:                 hex.EncodeToString(span.GetSpanId()),
					TraceState:             span.GetTraceState(),
					ParentSpanID:           sql.NullString{String: hex.EncodeToString(span.GetParentSpanId()), Valid: len(span.GetParentSpanId()) > 0},
					Name:                   span.GetName(),
					Kind:                   trace.SpanKind(span.GetKind()).String(),
					Flags:                  int64(span.GetFlags()),
					StartTime:              int64(span.GetStartTimeUnixNano()),
					EndTime:                sql.NullInt64{Int64: int64(span.GetEndTimeUnixNano()), Valid: span.GetEndTimeUnixNano() >= span.GetStartTimeUnixNano()},
					Attributes:             attrs,
					Events:                 events,
					Links:                  links,
					Resource:               res,
					InstrumentationScope:   instr,
					StatusCode:             int64(status),
					StatusMessage:          span.GetStatus().GetMessage(),
					DroppedAttributesCount: int64(span.GetDroppedAttributesCount()),
					DroppedEventsCount:     int64(span.GetDroppedEventsCount()),
					DroppedLinksCount:      int64(span.GetDroppedLinksCount()),
				})
			}
			if _, err := e.db.AppendSpans(rows); err != nil {
				return err
			}
		}
	}
	return nil
}

// Preserve the first received snapshot when a search combines stores: the
// live store is first, then imports in trace ID order. Parent placeholders
// are not received snapshots and must still be filled in.
func ingestInspectionSpanScope(ctx context.Context, primary, read *clientdb.DB, db *dagui.DB, scope map[string]struct{}) error {
	for id := range scope {
		if primary != read && primary.HasSpan(id) {
			delete(scope, id)
			continue
		}
		sid, err := trace.SpanIDFromHex(id)
		if err != nil {
			continue
		}
		if span := db.Spans.Map[dagui.SpanID{SpanID: sid}]; span != nil && span.Received {
			delete(scope, id)
		}
	}
	return ingestSpanScope(ctx, read, db, scope)
}

// Select exact spans without blending parent/link graphs from different
// captures. Live data wins the vanishingly rare collision with a snapshot.
func inspectionStoreForSpan(store *clientdb.DB, span string) *clientdb.DB {
	for _, candidate := range store.InspectionStores() {
		if candidate.HasSpan(span) {
			return candidate
		}
	}
	return store
}
