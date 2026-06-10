package snapshots

import (
	"context"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/distribution/reference"
	"go.opentelemetry.io/otel/log"
)

// DisplayRef shortens a fully qualified image ref for transfer span names
// ("pulling <ref>", "unpacking <ref>"): these surface as labeled progress
// rows in the TUI, where the repo digest and default registry are noise.
func DisplayRef(ref string) string {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return ref
	}
	trimmed := reference.TrimNamed(named)
	if tagged, ok := named.(reference.Tagged); ok {
		if withTag, err := reference.WithTag(trimmed, tagged.Tag()); err == nil {
			return reference.FamiliarString(withTag)
		}
	}
	return reference.FamiliarString(trimmed)
}

// ProgressEmitInterval throttles streaming progress records; the final
// record for an item is always emitted so consumers converge on the true
// completed state.
const ProgressEmitInterval = 100 * time.Millisecond

// EmitProgress sends one streaming-progress log record for the named item,
// attributed to the current span via ctx. See engine/telemetryattrs for the
// conventions.
func EmitProgress(ctx context.Context, item string, current, total int64) {
	rec := log.Record{}
	rec.SetTimestamp(time.Now())
	// Explicit empty body: log consumers skip empty-bodied records as text,
	// and an unset body does not survive the OTLP round-trip (nil AnyValue
	// triggers conversion errors on the receiving side).
	rec.SetBody(log.StringValue(""))
	rec.AddAttributes(
		log.String(telemetryattrs.ProgressItemAttr, item),
		log.Int64(telemetryattrs.ProgressCurrentAttr, current),
		log.Int64(telemetryattrs.ProgressTotalAttr, total),
		log.String(telemetryattrs.ProgressUnitAttr, "bytes"),
	)
	telemetry.Logger(ctx, "dagger.io/progress").Emit(ctx, rec)
}

// applyProgress streams compressed-bytes-read progress for one layer blob
// while it is decompressed and applied onto a snapshot, keyed by blob
// digest like the fetch emitter so the two phases line up cell for cell.
type applyProgress struct {
	ctx   context.Context
	item  string
	total int64

	mu       sync.Mutex
	lastEmit time.Time
}

func newApplyProgress(ctx context.Context, item string, total int64) *applyProgress {
	ap := &applyProgress{
		ctx:   ctx,
		item:  item,
		total: total,
	}
	EmitProgress(ctx, item, 0, total)
	return ap
}

func (ap *applyProgress) update(read int64) {
	ap.mu.Lock()
	now := time.Now()
	// purely throttled: finish guarantees the final state
	if now.Sub(ap.lastEmit) < ProgressEmitInterval {
		ap.mu.Unlock()
		return
	}
	ap.lastEmit = now
	ap.mu.Unlock()
	EmitProgress(ap.ctx, ap.item, read, ap.total)
}

func (ap *applyProgress) finish() {
	EmitProgress(ap.ctx, ap.item, ap.total, ap.total)
}
