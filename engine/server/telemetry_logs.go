package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	telemetry "github.com/dagger/otel-go"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// On-demand log reads over a client's own live store. A frontend that
// renders only some spans subscribes to the display-free push
// (GET /v1/logs?display=presence) and reads display text for the spans it
// renders (POST /v1/logs/query), instead of receiving and keeping every log
// body. Each request reads only the requesting client's store, like the
// subscriptions; a nested client reads its own.

// presenceLogFetcher is the display-free log push: every row but display
// text, which it replaces with one presence record per (trace, span, digest).
// Its presence set belongs to one stream; a reconnect starts a new one and may
// repeat presence, which is idempotent.
func presenceLogFetcher() streamFetcher {
	seen := clientdb.NewLogPresenceSet()
	lastSince := int64(-1)
	var last []clientdb.LogPresence
	return func(ctx context.Context, db *clientdb.DB, since int64, limit int) (int64, proto.Message, int, error) {
		if since == lastSince {
			// The stream discarded the last batch (too large) and is
			// refetching a smaller one at the same cursor.
			seen.Forget(last)
		}
		last, lastSince = nil, -1
		through := db.HighWater().Logs
		if since >= through {
			// Idle: the store has no new rows, so do nothing else.
			return since, nil, 0, nil
		}
		rows, presence, next, err := db.SelectLogsWithoutDisplay(ctx, clientdb.SelectLogsRangeParams{AfterID: since, ThroughID: through, Limit: int64(limit)}, seen)
		if err != nil {
			return 0, nil, 0, err
		}
		last, lastSince = presence, since
		req := &collogspb.ExportLogsServiceRequest{ResourceLogs: clientdb.LogsToPB(rows)}
		if len(presence) > 0 {
			req.ResourceLogs = append(req.ResourceLogs, presenceResourceLogs(presence))
		}
		return next, req, int(next - since), nil
	}
}

// presenceResourceLogs encodes presence as body-less records carrying the
// LogRolePresence role and, for digest-routed rows, their digest.
func presenceResourceLogs(presence []clientdb.LogPresence) *otlplogsv1.ResourceLogs {
	records := make([]*otlplogsv1.LogRecord, 0, len(presence))
	for _, p := range presence {
		traceID, err := hex.DecodeString(p.TraceID)
		if err != nil || len(traceID) != 16 {
			continue
		}
		spanID, err := hex.DecodeString(p.SpanID)
		if err != nil || len(spanID) != 8 {
			continue
		}
		attrs := []*otlpcommonv1.KeyValue{{
			Key:   telemetryattrs.LogRoleAttr,
			Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: telemetryattrs.LogRolePresence}},
		}}
		if p.Digest != "" {
			attrs = append(attrs, &otlpcommonv1.KeyValue{
				Key:   telemetry.DagDigestAttr,
				Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: p.Digest}},
			})
		}
		records = append(records, &otlplogsv1.LogRecord{
			TimeUnixNano: uint64(p.Timestamp), //nolint:gosec // stored from an OTLP uint64
			TraceId:      traceID,
			SpanId:       spanID,
			Body:         &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{}},
			Attributes:   attrs,
		})
	}
	return &otlplogsv1.ResourceLogs{ScopeLogs: []*otlplogsv1.ScopeLogs{{LogRecords: records}}}
}

// displayLogFetcher reads the display text of spans. A one-shot read ends at
// the store's end when it first runs; a follow reads to the current end on
// every poll. The scope is resolved once and then follows new spans itself, so
// an idle poll (no new rows) costs one high-water read.
func displayLogFetcher(spans []archive.LogSpan, oneShot bool) streamFetcher {
	var scope *clientdb.LogQueryScope
	through := int64(-1)
	return func(ctx context.Context, db *clientdb.DB, since int64, limit int) (int64, proto.Message, int, error) {
		end := through
		if end < 0 {
			end = db.HighWater().Logs
			if oneShot {
				through = end
			}
		}
		if since >= end {
			return since, nil, 0, nil
		}
		if scope == nil {
			scope = db.LiveLogScope(spans)
		}
		rows, next, err := db.SelectDisplayLogs(ctx, clientdb.SelectLogsRangeParams{AfterID: since, ThroughID: end, Limit: int64(limit)}, "", scope)
		if err != nil {
			return 0, nil, 0, err
		}
		return next, &collogspb.ExportLogsServiceRequest{ResourceLogs: clientdb.LogsToPB(rows)}, int(next - since), nil
	}
}

// LogsQueryHandler serves POST /v1/logs/query: the display text of the spans
// an archive.LogQuery body names, over the framed binary stream with row ID
// cursors (LiveCursorHeader resumes). A one-shot query ends with a terminal
// frame at the store's end as of the request; a follow ends when the client
// goes away or, with a terminal frame, when the client's telemetry drains.
func (ps *PubSub) LogsQueryHandler(w http.ResponseWriter, r *http.Request, record *clientRecord) error {
	if !acceptsBinaryTelemetry(r.Header.Get("Accept")) {
		return rejectLogRequest(w, errors.New("log queries require "+enginetel.LiveContentType), http.StatusNotAcceptable)
	}
	var q archive.LogQuery
	if err := archive.DecodeLogBody(r.Body, &q); err != nil {
		return rejectLogRequest(w, err, http.StatusBadRequest)
	}
	if err := q.Validate(); err != nil {
		return rejectLogRequest(w, err, http.StatusBadRequest)
	}
	return ps.serveLiveStream(w, r, record, displayLogFetcher(q.Spans, !q.Follow), enginetel.MaxLivePayloadSize, liveStreamOptions{oneShot: !q.Follow})
}

// rejectLogRequest answers a request that can never succeed as sent. It
// writes the status itself: httpHandlerFunc probes the writer with an empty
// Write first, which commits a 200 on a real connection.
func rejectLogRequest(w http.ResponseWriter, err error, code int) error {
	http.Error(w, err.Error(), code)
	return nil
}

// LogsSearchHandler serves POST /v1/logs/search: an archive.LogSearch over
// the client's store up to its current end, answered as one JSON
// archive.LogSearchResult page.
func (ps *PubSub) LogsSearchHandler(w http.ResponseWriter, r *http.Request, record *clientRecord) error {
	var search archive.LogSearch
	if err := archive.DecodeLogBody(r.Body, &search); err != nil {
		return rejectLogRequest(w, err, http.StatusBadRequest)
	}
	if _, err := search.Matcher(); err != nil {
		return rejectLogRequest(w, err, http.StatusBadRequest)
	}
	db, err := record.TelemetryDB(r.Context())
	if err != nil {
		return err
	}
	defer db.Close()
	var scope *clientdb.LogQueryScope
	if len(search.Spans) > 0 {
		scope = db.LiveLogScope(search.Spans)
	}
	result, err := db.SearchLogs(r.Context(), search, db.HighWater().Logs, "", scope)
	if err != nil {
		return err
	}
	return writeLogSearchResult(w, result)
}

func writeLogSearchResult(w http.ResponseWriter, result archive.LogSearchResult) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	return json.NewEncoder(w).Encode(result)
}
