package server

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// maxSpanQueryBody bounds a span query's JSON body.
const maxSpanQueryBody = 4 << 20

// spanQueryFrameRows is how many spans each frame of a span query's answer
// carries, keeping frames well under enginetel.MaxLivePayloadSize.
const spanQueryFrameRows = 2_000

// SpansQueryHandler serves POST enginetel.LiveSpanQueryPath: the newest
// snapshots of the spans a clientdb.SpanQuery body selects from the client's
// own live store, as OTLP trace frames over the framed binary stream, each
// span stamped with its child count (telemetryattrs.UIChildCountAttr) and
// whether it has display text (UIHasLogsAttr). Every frame's cursor is the
// store's span high-water mark as of the query, which the terminal frame
// repeats. The answer is read from the store's indexes: its cost is the
// result's, not the trace's.
func (ps *PubSub) SpansQueryHandler(w http.ResponseWriter, r *http.Request, record *clientRecord) error {
	if !acceptsBinaryTelemetry(r.Header.Get("Accept")) {
		return rejectLogRequest(w, errors.New("span queries require "+enginetel.LiveContentType), http.StatusNotAcceptable)
	}
	var q clientdb.SpanQuery
	if err := json.NewDecoder(io.LimitReader(r.Body, maxSpanQueryBody)).Decode(&q); err != nil {
		return rejectLogRequest(w, err, http.StatusBadRequest)
	}
	if len(q.Spans) == 0 {
		return rejectLogRequest(w, errors.New("span query names no spans"), http.StatusBadRequest)
	}
	if len(q.Spans) > clientdb.MaxSpanQuerySpans {
		return rejectLogRequest(w, errors.New("span query names too many spans"), http.StatusBadRequest)
	}
	db, err := record.TelemetryDB(r.Context())
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := db.QuerySpans(r.Context(), q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil
	}
	w.Header().Set("Content-Type", enginetel.LiveContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(enginetel.LiveFeaturesHeader, liveTelemetryFeatures)
	for start := 0; start < len(result.Spans); start += spanQueryFrameRows {
		rows := result.Spans[start:min(start+spanQueryFrameRows, len(result.Spans))]
		payload, err := proto.Marshal(spanQueryBatch(rows, result))
		if err != nil {
			return err
		}
		if err := enginetel.WriteLiveFrame(w, result.Through, payload); err != nil {
			return err
		}
	}
	return enginetel.WriteLiveTerminal(w, result.Through)
}

// spanQueryBatch encodes rows as OTLP, stamped with what result knows of
// the spans beneath each.
func spanQueryBatch(rows []clientdb.Span, result clientdb.SpanQueryResult) *coltracepb.ExportTraceServiceRequest {
	resourceSpans := clientdb.SpansToPB(rows)
	for _, rs := range resourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, span := range ss.Spans {
				stampSpanQueryAttrs(span, result)
			}
		}
	}
	return &coltracepb.ExportTraceServiceRequest{ResourceSpans: resourceSpans}
}

func stampSpanQueryAttrs(span *otlptracev1.Span, result clientdb.SpanQueryResult) {
	id := hex.EncodeToString(span.SpanId)
	span.Attributes = append(span.Attributes, &otlpcommonv1.KeyValue{
		Key:   telemetryattrs.UIChildCountAttr,
		Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_IntValue{IntValue: int64(result.ChildCounts[id])}},
	})
	if result.HasLogs[id] {
		span.Attributes = append(span.Attributes, &otlpcommonv1.KeyValue{
			Key:   telemetryattrs.UIHasLogsAttr,
			Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BoolValue{BoolValue: true}},
		})
	}
}
