package dagui

import (
	"github.com/dagger/dagger/engine/telemetryattrs"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// ProgressItem is the latest streaming-progress state for one named item of
// work within a span - e.g. one image layer being fetched.
type ProgressItem struct {
	Name    string
	Current int64
	Total   int64
	Unit    string
}

// Complete reports whether the item has reached its known total.
func (item *ProgressItem) Complete() bool {
	return item.Total > 0 && item.Current >= item.Total
}

// SpanProgress aggregates the progress items reported by a span, ordered by
// first appearance.
type SpanProgress struct {
	Order  []*ProgressItem
	byName map[string]*ProgressItem
}

func (p *SpanProgress) update(name string, current, total int64, unit string) {
	if p.byName == nil {
		p.byName = map[string]*ProgressItem{}
	}
	item, ok := p.byName[name]
	if !ok {
		item = &ProgressItem{Name: name}
		p.byName[name] = item
		p.Order = append(p.Order, item)
	}
	item.Current = current
	item.Total = total
	if unit != "" {
		item.Unit = unit
	}
}

// Totals sums current and total across all items. Items with unknown totals
// contribute only to current.
func (p *SpanProgress) Totals() (current, total int64) {
	for _, item := range p.Order {
		current += item.Current
		total += item.Total
	}
	return current, total
}

// ingestProgress folds a streaming-progress log record (one carrying
// telemetryattrs.ProgressItemAttr) into the target span's progress state.
// It reports whether the record was progress data; such records are consumed
// entirely and must not be treated as log text.
func (db *DB) ingestProgress(record sdklog.Record) bool {
	var item, unit string
	var current, total int64
	record.WalkAttributes(func(kv otellog.KeyValue) bool {
		switch kv.Key {
		case telemetryattrs.ProgressItemAttr:
			item = kv.Value.AsString()
		case telemetryattrs.ProgressCurrentAttr:
			current = kv.Value.AsInt64()
		case telemetryattrs.ProgressTotalAttr:
			total = kv.Value.AsInt64()
		case telemetryattrs.ProgressUnitAttr:
			unit = kv.Value.AsString()
		}
		return true
	})
	if item == "" {
		return false
	}

	spanID := SpanID{SpanID: record.SpanID()}
	if !spanID.IsValid() {
		return true
	}
	span := db.initSpan(spanID)
	if span.Progress == nil {
		span.Progress = &SpanProgress{}
	}
	span.Progress.update(item, current, total, unit)

	// Surface the progress on ancestors so collapsed rows (or rows whose
	// progress-carrying descendants are hidden) can render it.
	for parent := span.ParentSpan; parent != nil; parent = parent.ParentSpan {
		parent.ProgressSpans.Add(span)
	}

	db.update(span)
	return true
}
