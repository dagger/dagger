package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	telemetry "github.com/dagger/otel-go"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// A client posts the telemetry its own process emits -- a CLI's command span,
// its output, its connect spans -- over its session connection (see
// engine.ClientTelemetryIngestHeader), so that the client's store holds all of
// the client's telemetry, and the engine publishes it to Cloud with the rest.
// It takes the same path as what the client's containers post, stamped with
// TelemetryClientIngestedAttr, which keeps it out of the client's own streams:
// the client has it already.

// clientIngestHandler serves a client's POST of its own telemetry with
// ingest. It writes its own error responses: the client needs the status to
// know its telemetry did not land.
func (ps *PubSub) clientIngestHandler(ingest func(*http.Request, *clientRecord) error, record *clientRecord) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ingest(r, record); err != nil {
			code := http.StatusInternalServerError
			var herr httpError
			if errors.As(err, &herr) {
				code = herr.code
			}
			slog.Warn("error ingesting client telemetry", "client", record.clientID, "path", r.URL.Path, "err", err)
			http.Error(w, err.Error(), code)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}
}

// ingestClientTraces stores the spans a client posts about itself.
func (ps *PubSub) ingestClientTraces(r *http.Request, record *clientRecord) error {
	var req coltracepb.ExportTraceServiceRequest
	if err := readClientOTLP(r, &req); err != nil {
		return err
	}
	enginetel.NormalizeTraceRequest(&req)
	for _, rs := range req.GetResourceSpans() {
		rs.Resource = withClientIngested(rs.GetResource(), record.clientID)
	}
	spans := telemetry.SpansFromPB(req.GetResourceSpans())
	if err := record.daggerSession.postedSpanExporter(record.clientID).ExportSpans(r.Context(), spans); err != nil {
		return fmt.Errorf("export %d spans: %w", len(spans), err)
	}
	return nil
}

// ingestClientLogs stores the log records a client posts about itself.
func (ps *PubSub) ingestClientLogs(r *http.Request, record *clientRecord) error {
	var req collogspb.ExportLogsServiceRequest
	if err := readClientOTLP(r, &req); err != nil {
		return err
	}
	enginetel.NormalizeLogsRequest(&req)
	for _, rl := range req.GetResourceLogs() {
		rl.Resource = withClientIngested(rl.GetResource(), record.clientID)
	}
	if err := telemetry.ReexportLogsFromPB(r.Context(), record.daggerSession.postedLogExporter(record.clientID), &req); err != nil {
		return fmt.Errorf("export logs: %w", err)
	}
	return nil
}

// ingestClientMetrics stores the metrics a client posts about itself.
func (ps *PubSub) ingestClientMetrics(r *http.Request, record *clientRecord) error {
	var req colmetricspb.ExportMetricsServiceRequest
	if err := readClientOTLP(r, &req); err != nil {
		return err
	}
	enginetel.NormalizeMetricsRequest(&req)
	for _, rm := range req.GetResourceMetrics() {
		rm.Resource = withClientIngested(rm.GetResource(), record.clientID)
	}
	exporters := record.daggerSession.postedMetricExporters(clientMetricExporter{record: record, ps: ps})
	if err := enginetel.ReexportMetricsFromPB(r.Context(), exporters, &req); err != nil {
		return fmt.Errorf("export metrics: %w", err)
	}
	return nil
}

func readClientOTLP(r *http.Request, msg proto.Message) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return httpErr(fmt.Errorf("read body: %w", err), http.StatusBadRequest)
	}
	if err := proto.Unmarshal(body, msg); err != nil {
		return httpErr(fmt.Errorf("unmarshal OTLP request: %w", err), http.StatusBadRequest)
	}
	return nil
}

// withClientIngested returns res with TelemetryClientIngestedAttr set to
// clientID, replacing any value the client sent.
func withClientIngested(res *otlpresourcev1.Resource, clientID string) *otlpresourcev1.Resource {
	if res == nil {
		res = &otlpresourcev1.Resource{}
	}
	attrs := make([]*otlpcommonv1.KeyValue, 0, len(res.GetAttributes())+1)
	for _, kv := range res.GetAttributes() {
		if kv.GetKey() != telemetryattrs.TelemetryClientIngestedAttr {
			attrs = append(attrs, kv)
		}
	}
	attrs = append(attrs, &otlpcommonv1.KeyValue{
		Key: telemetryattrs.TelemetryClientIngestedAttr,
		Value: &otlpcommonv1.AnyValue{
			Value: &otlpcommonv1.AnyValue_StringValue{StringValue: clientID},
		},
	})
	res.Attributes = attrs
	return res
}

var clientIngestedAttrKey = []byte(`"` + telemetryattrs.TelemetryClientIngestedAttr + `"`)

// ingestedFrom reports whether a resource carries clientID as its
// TelemetryClientIngestedAttr.
func ingestedFrom(res *otlpresourcev1.Resource, clientID string) bool {
	for _, kv := range res.GetAttributes() {
		if kv.GetKey() == telemetryattrs.TelemetryClientIngestedAttr {
			return kv.GetValue().GetStringValue() == clientID
		}
	}
	return false
}

// ingestedResourceRow reports whether a span or log row's stored resource
// says the row was ingested from clientID. The byte search keeps the common
// case cheap; a hit is confirmed by decoding.
func ingestedResourceRow(resource []byte, clientID string) bool {
	if !bytes.Contains(resource, clientIngestedAttrKey) {
		return false
	}
	var res otlpresourcev1.Resource
	if err := protojson.Unmarshal(resource, &res); err != nil {
		return false
	}
	return ingestedFrom(&res, clientID)
}

// ingestedMetricRow is ingestedResourceRow for a metric row, which stores a
// whole ResourceMetrics.
func ingestedMetricRow(data []byte, clientID string) bool {
	if !bytes.Contains(data, clientIngestedAttrKey) {
		return false
	}
	var rm otlpmetricsv1.ResourceMetrics
	if err := protojson.Unmarshal(data, &rm); err != nil {
		return false
	}
	return ingestedFrom(rm.GetResource(), clientID)
}

// streamedSpanRow, streamedLogRow and streamedMetricRow select the rows of a
// client's store that its own telemetry streams carry: all but the ones
// ingested from that very client.
func streamedSpanRow(clientID string) func(*clientdb.Span) bool {
	return func(row *clientdb.Span) bool { return !ingestedResourceRow(row.Resource, clientID) }
}

func streamedLogRow(clientID string) func(*clientdb.Log) bool {
	return func(row *clientdb.Log) bool { return !ingestedResourceRow(row.Resource, clientID) }
}

func streamedMetricRow(clientID string) func(*clientdb.Metric) bool {
	return func(row *clientdb.Metric) bool { return !ingestedMetricRow(row.Data, clientID) }
}
