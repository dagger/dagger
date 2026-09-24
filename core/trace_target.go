package core

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"
)

// resolveTraceTarget validates and normalizes a ReadTrace span argument, then
// checks the live and imported telemetry indexes without loading a span tree.
func resolveTraceTarget(ctx context.Context, span string) (string, error) {
	span = normalizeSpanArg(span)
	if span == "" {
		return "", fmt.Errorf("ReadTrace needs a span ID; use FindSpans first to find a check, test, or other step by name")
	}
	if _, err := trace.SpanIDFromHex(span); err != nil {
		return "", fmt.Errorf("invalid span ID %q: %w", span, err)
	}
	clientDB, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer clientDB.Close()
	for _, read := range clientDB.InspectionStores() {
		if read.HasSpan(span) {
			return span, nil
		}
	}
	return "", fmt.Errorf("no span %q in this trace; use FindSpans to find a span ID", span)
}
