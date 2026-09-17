package dagql

import (
	"context"

	"github.com/dagger/dagger/engine/slog"
)

// partReselectWarnAfter is how many iterations a reselect loop makes before
// its first warning. Ordinary contention, a momentarily busy store guard or a
// racing publication, resolves in a handful of iterations; a loop that gets
// this far is almost certainly answering a refusal that will never change.
const partReselectWarnAfter = 1 << 14

// partReselectWatch observes one unbounded reselect loop; all seven carry one.
// It is a diagnostic only: it never fails, delays or otherwise changes the
// request. Once the loop has gone round partReselectWarnAfter times it logs a
// warning carrying the last refusal cause, which names the refusing site (see
// partRefusal), and again at each doubling. A deterministic refusal
// answered with a reselect, which these loops retry forever, then shows up in
// the engine log instead of as a silent spin.
type partReselectWatch struct {
	loop  string
	n     uint64
	next  uint64
	cause error
}

// refused records the cause of the refusal the loop is about to retry.
func (w *partReselectWatch) refused(cause error) {
	if cause != nil {
		w.cause = cause
	}
}

// again is called once per iteration.
func (w *partReselectWatch) again(ctx context.Context, row *sharedResult, address PersistedPartAddress) {
	w.n++
	if w.next == 0 {
		w.next = partReselectWarnAfter
	}
	if w.n < w.next {
		return
	}
	w.next *= 2
	var id uint64
	if row != nil {
		id = uint64(row.id)
	}
	slog.WarnContext(ctx, "part reselect loop is not making progress",
		"loop", w.loop,
		"iterations", w.n,
		"result", id,
		"part", string(address.Part),
		"lastCause", w.cause,
	)
}
