package core

import (
	"database/sql"
	"fmt"
	"strings"

	"context"

	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

// sync this with idtui.llmLogsLastLines to ensure user and LLM sees the same
// thing
const llmLogsLastLines = 8
const llmLogsMaxLineLen = 2000
const llmLogsBatchSize = 1000

// captureLogs returns nicely formatted lines of all logs seen since the
// last capture.
func captureLogs(ctx context.Context, spanID string) ([]string, error) {
	root, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	mainMeta, err := root.MainClientCallerMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("get main client caller metadata: %w", err)
	}
	q, err := root.ClientTelemetry(ctx, mainMeta.SessionID, mainMeta.ClientID)
	if err != nil {
		return nil, err
	}
	defer q.Close()

	buf := new(strings.Builder)

	var lastLogID int64

	for {
		logs, err := q.SelectLogsBeneathSpan(ctx, clientdb.SelectLogsBeneathSpanParams{
			ID:     lastLogID,
			SpanID: sql.NullString{Valid: true, String: spanID},
			Limit:  llmLogsBatchSize,
		})
		if err != nil {
			return nil, err
		}
		if len(logs) == 0 {
			break
		}

		for _, log := range logs {
			lastLogID = log.ID

			var logAttrs []*otlpcommonv1.KeyValue
			if err := clientdb.UnmarshalProtoJSONs(log.Attributes, &otlpcommonv1.KeyValue{}, &logAttrs); err != nil {
				slog.Warn("failed to unmarshal log attributes", "error", err)
				continue
			}
			var skip bool
		dance:
			for _, attr := range logAttrs {
				switch attr.Key {
				case telemetry.StdioEOFAttr, telemetry.LogsVerboseAttr, telemetry.LogsGlobalAttr:
					if attr.Value.GetBoolValue() {
						skip = true
						break dance
					}
				}
			}
			if skip {
				continue
			}

			if log.SpanID.Valid {
				span, err := q.SelectSpan(ctx, clientdb.SelectSpanParams{
					TraceID: log.TraceID.String,
					SpanID:  log.SpanID.String,
				})
				if err != nil {
					return nil, err
				}
				var spanAttrs []*otlpcommonv1.KeyValue
				if err := clientdb.UnmarshalProtoJSONs(span.Attributes, &otlpcommonv1.KeyValue{}, &spanAttrs); err != nil {
					slog.Warn("failed to unmarshal span attributes", "error", err)
					continue
				}
				var isNoise bool
				for _, attr := range spanAttrs {
					if attr.Key == telemetry.LLMRoleAttr || attr.Key == telemetry.LLMToolAttr {
						isNoise = true
						break
					}
				}
				if isNoise {
					continue
				}
			}

			var bodyPb otlpcommonv1.AnyValue
			if err := proto.Unmarshal(log.Body, &bodyPb); err != nil {
				slog.Warn("failed to unmarshal log body", "error", err, "client", mainMeta.ClientID, "log", log.ID)
				continue
			}
			switch x := bodyPb.GetValue().(type) {
			case *otlpcommonv1.AnyValue_StringValue:
				fmt.Fprint(buf, x.StringValue)
			case *otlpcommonv1.AnyValue_BytesValue:
				buf.Write(x.BytesValue)
			default:
				fmt.Fprintf(buf, "UNHANDLED: %+v", x)
			}
		}
	}
	if buf.Len() == 0 {
		return nil, nil
	}
	return strings.Split(
		strings.TrimRight(buf.String(), "\n"),
		"\n",
	), nil
}

func limitLines(spanID string, logs []string, limit, maxLineLen int) []string {
	if limit > 0 && len(logs) > limit {
		snipped := fmt.Sprintf("... %d lines omitted (use ReadLogs(span: %s) to read more) ...", len(logs)-limit, spanID)
		logs = append([]string{snipped}, logs[len(logs)-limit:]...)
	}
	for i, line := range logs {
		if len(line) > maxLineLen {
			logs[i] = line[:maxLineLen] + fmt.Sprintf("[... %d chars truncated]", len(line)-maxLineLen)
		}
	}
	return logs
}
