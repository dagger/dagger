package dagui_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/dagger/dagger/dagql/dagui"
	telemetry "github.com/dagger/otel-go"
)

func llmTokenPoint(model, span string, value int64) metricdata.DataPoint[int64] {
	return metricdata.DataPoint[int64]{
		Attributes: attribute.NewSet(
			attribute.String("model", model),
			attribute.String("provider", "test"),
			attribute.String(telemetry.MetricsSpanIDAttr, span),
		),
		Value: value,
	}
}

func TestLLMTokenMetricsAggregatesGaugeLastValues(t *testing.T) {
	metrics := &dagui.LLMTokenMetrics{}

	metrics.Aggregate(telemetry.LLMInputTokens, llmTokenPoint("model-a", "span-1", 10))
	metrics.Aggregate(telemetry.LLMInputTokens, llmTokenPoint("model-a", "span-1", 10))
	metrics.Aggregate(telemetry.LLMInputTokens, llmTokenPoint("model-a", "span-1", 15))
	metrics.Aggregate(telemetry.LLMInputTokens, llmTokenPoint("model-a", "span-2", 7))

	snapshot := metrics.Snapshot()
	require.Len(t, snapshot, 1)
	require.Equal(t, int64(22), snapshot[0].InputTokens)
}

func TestLLMTokenMetricsTreatsGaugeDropAsFreshBaseline(t *testing.T) {
	metrics := &dagui.LLMTokenMetrics{}

	metrics.Aggregate(telemetry.LLMInputTokens, llmTokenPoint("model-a", "span-1", 10))
	metrics.Aggregate(telemetry.LLMInputTokens, llmTokenPoint("model-a", "span-1", 3))

	snapshot := metrics.Snapshot()
	require.Len(t, snapshot, 1)
	require.Equal(t, int64(13), snapshot[0].InputTokens)
}
