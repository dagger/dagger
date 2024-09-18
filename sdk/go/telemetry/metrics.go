package telemetry

import (
	"context"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/log"
)

func NewSpanMetrics(ctx context.Context, name string) *SpanMetrics {
	return &SpanMetrics{
		ctx:    ctx,
		logger: Logger(ctx, name),
	}
}

type SpanMetrics struct {
	ctx    context.Context
	logger log.Logger
}

func (sm *SpanMetrics) EmitDiskReadBytes(v int) {
	sm.emitMetric(v, DiskReadBytesAttr)
}

func (sm *SpanMetrics) EmitDiskWriteBytes(v int) {
	sm.emitMetric(v, DiskWriteBytesAttr)
}

func (sm *SpanMetrics) emitMetric(v int, attrVal string) {
	rec := log.Record{}
	rec.SetTimestamp(time.Now())
	// rec.SetBody(log.IntValue(v))
	rec.SetBody(log.StringValue(strconv.Itoa(v)))
	rec.AddAttributes(log.String(MetricsAttr, attrVal))
	sm.logger.Emit(sm.ctx, rec)
}
