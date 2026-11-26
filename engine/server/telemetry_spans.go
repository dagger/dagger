package server

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// maxSpanQueryBody bounds a span query's JSON body.
const maxSpanQueryBody = 4 << 20

// spanQueryFrameRows bounds the number of rows encoded at once. Large
// snapshots may need smaller batches to fit the wire limit.
const spanQueryFrameRows = 2_000

// SpansQueryHandler serves POST enginetel.LiveSpanQueryPath: the newest
// snapshots of the spans a clientdb.SpanQuery body selects from the client's
// own live store, as OTLP trace frames over the framed binary stream, each
// span stamped with its child count (telemetryattrs.UIChildCountAttr) and
// whether it has display text (UIHasLogsAttr). Every frame's cursor is the
// store's span high-water mark as of the query, which the terminal frame
// repeats. If any span can't be encoded, all spans that fit are delivered,
// followed by an error frame at that cursor instead of a terminal frame.
// The answer is read from the store's indexes: its cost is the
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
	if err := writeSpanQueryFrames(w, result, enginetel.MaxLivePayloadSize); err != nil {
		// The connection failed mid-stream. Returning err would have
		// httpHandlerFunc append a plaintext HTTP error to a binary frame.
		slog.Debug("write span query response", "err", err)
	}
	return nil
}

// writeSpanQueryFrames delivers all encodable spans, then a terminal frame
// on success or an error frame describing the first unencodable span.
func writeSpanQueryFrames(w io.Writer, result clientdb.SpanQueryResult, maxBytes int) error {
	var firstErr error
	var writeBatch func(*coltracepb.ExportTraceServiceRequest) error
	writeBatch = func(batch *coltracepb.ExportTraceServiceRequest) error {
		size := proto.Size(batch)
		var err error
		if size <= maxBytes {
			var payload []byte
			payload, err = proto.MarshalOptions{UseCachedSize: true}.Marshal(batch)
			if err == nil {
				return enginetel.WriteLiveFrame(w, result.Through, payload)
			}
		} else {
			err = fmt.Errorf("payload is %d bytes (maximum %d)", size, maxBytes)
		}
		// Split the OTLP tree, not the rows: decoding a large attribute
		// again at every split can cost many times the size of the answer.
		write := func() error { return writeBatch(batch) }
		if len(batch.ResourceSpans) > 1 {
			return writeSpanQueryHalves(&batch.ResourceSpans, write)
		}
		rs := batch.ResourceSpans[0]
		if len(rs.ScopeSpans) > 1 {
			return writeSpanQueryHalves(&rs.ScopeSpans, write)
		}
		ss := rs.ScopeSpans[0]
		if len(ss.Spans) > 1 {
			return writeSpanQueryHalves(&ss.Spans, write)
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("encode span %x: %w", ss.Spans[0].SpanId, err)
		}
		return nil
	}
	for rows := range slices.Chunk(result.Spans, spanQueryFrameRows) {
		if err := writeBatch(spanQueryBatch(rows, result)); err != nil {
			return err
		}
	}
	if firstErr != nil {
		return enginetel.WriteLiveError(w, result.Through, firstErr)
	}
	return enginetel.WriteLiveTerminal(w, result.Through)
}

// writeSpanQueryHalves presents each half of an OTLP repeated field to
// write, retaining its enclosing resource and scope in both frames.
func writeSpanQueryHalves[T any](field *[]T, write func() error) error {
	all := *field
	defer func() { *field = all }()
	mid := len(all) / 2
	*field = all[:mid]
	if err := write(); err != nil {
		return err
	}
	*field = all[mid:]
	return write()
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
