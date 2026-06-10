package snapshots

import (
	"context"
	"io"
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
// attributed to the current span via ctx. The unit names what current/total
// count, e.g. "bytes" or "objects"; see engine/telemetryattrs for the
// conventions.
func EmitProgress(ctx context.Context, item string, current, total int64, unit string) {
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
	)
	if unit != "" {
		rec.AddAttributes(log.String(telemetryattrs.ProgressUnitAttr, unit))
	}
	telemetry.Logger(ctx, "dagger.io/progress").Emit(ctx, rec)
}

// progressTracker streams one item's absolute progress with throttled
// updates; an initial zero state is emitted immediately so the item appears
// as soon as work begins, and finish emits the converged final state.
type progressTracker struct {
	ctx   context.Context
	item  string
	total int64

	mu       sync.Mutex
	lastEmit time.Time
}

func newProgressTracker(ctx context.Context, item string, total int64) *progressTracker {
	pt := &progressTracker{
		ctx:   ctx,
		item:  item,
		total: total,
	}
	EmitProgress(ctx, item, 0, total, "bytes")
	return pt
}

func (pt *progressTracker) update(current int64) {
	pt.mu.Lock()
	now := time.Now()
	// purely throttled: finish guarantees the final state
	if now.Sub(pt.lastEmit) < ProgressEmitInterval {
		pt.mu.Unlock()
		return
	}
	pt.lastEmit = now
	pt.mu.Unlock()
	EmitProgress(pt.ctx, pt.item, current, pt.total, "bytes")
}

func (pt *progressTracker) finish() {
	EmitProgress(pt.ctx, pt.item, pt.total, pt.total, "bytes")
}

// NewProgressReader wraps r to stream its read progress as one item via the
// telemetry convention, attributed to the span carried by ctx. A total <= 0
// means the size is unknown (indeterminate). The final state is emitted
// when the reader sees EOF or is closed.
func NewProgressReader(ctx context.Context, item string, total int64, r io.ReadCloser) io.ReadCloser {
	return &progressReader{
		r:       r,
		tracker: newProgressTracker(ctx, item, max(total, 0)),
	}
}

type progressReader struct {
	r       io.ReadCloser
	tracker *progressTracker

	read int64
	done bool
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.read += int64(n)
		pr.tracker.update(pr.read)
	}
	if err == io.EOF {
		pr.emitFinal()
	}
	return n, err
}

func (pr *progressReader) Close() error {
	pr.emitFinal()
	return pr.r.Close()
}

// emitFinal emits the converged final state once: everything read, against
// the known total if there is one.
func (pr *progressReader) emitFinal() {
	if pr.done {
		return
	}
	pr.done = true
	total := pr.tracker.total
	if total <= 0 {
		total = pr.read
	}
	EmitProgress(pr.tracker.ctx, pr.tracker.item, pr.read, total, "bytes")
}
