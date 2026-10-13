package slog

import (
	"bytes"
	"io"
	"sync"
)

// Writer queues log output for a shared destination (usually stderr) and
// writes it from one background goroutine, so goroutines that log at the
// same time do not each wait for every other goroutine's write(2).
//
// Bytes reach the destination in the order Write calls queued them. Each
// Write is queued whole, so a log line is never split or interleaved with
// another line written through the Writer. Queued bytes, including the batch
// being written, are bounded by limit (a single larger line is let through
// alone): Write blocks while the queue is full, so a destination that cannot
// keep up slows callers down instead of losing output.
//
// Write returns once its bytes are queued, except for lines whose level is
// WARN or worse when SyncWarn is set; those wait until they are written, as
// with a plain locked writer. Flush waits too. A destination
// write error loses the rest of that batch, as a failed direct write loses
// its line; callers waiting on those bytes get the error and the count of
// their bytes that were written.
//
// A Writer runs one goroutine for the life of the process and has no Close:
// it is meant for the process's own log output.
type Writer struct {
	out   io.Writer
	limit int

	// SyncWarn makes Write wait for lines at WARN level or worse.
	SyncWarn bool

	mu       sync.Mutex
	cond     sync.Cond // broadcast after each batch, for writers waiting on the limit
	pending  []byte
	spare    []byte
	inflight int    // length of the batch being written
	queued   uint64 // stream offset after the last queued byte
	written  uint64 // stream offset through which batches have been written (or failed)
	waiters  map[*waiter]struct{}
	// pendingBatch and inflightBatch, when not nil, are closed once the
	// queued bytes and the batch being written are done. They are made only
	// when someone waits, and each wakes only the waiters of its batch.
	pendingBatch  chan struct{}
	inflightBatch chan struct{}
	kick          chan struct{}
}

// A waiter is a caller that will learn the outcome of bytes queued at or
// after start.
type waiter struct {
	start uint64
	// first is the first failed batch ending after start. Batches complete
	// in order, so it is the only failure that can concern the caller: a
	// later one starts after it.
	first *failure
}

// failure records a batch the destination did not take whole, or took whole
// but still reported an error for.
type failure struct {
	start, end uint64 // the batch's stream offsets
	through    uint64 // offset through which bytes were written
	err        error
}

// NewWriter returns a Writer to out that queues at most limit bytes.
func NewWriter(out io.Writer, limit int) *Writer {
	w := &Writer{out: out, limit: limit, kick: make(chan struct{}, 1), waiters: map[*waiter]struct{}{}}
	w.cond.L = &w.mu
	go w.drain()
	return w
}

func (w *Writer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if w.SyncWarn && isWarnOrWorse(p) {
		return w.writeSync(p)
	}
	w.enqueue(p, false)
	return len(p), nil
}

// writeSync queues p and waits until it is written.
func (w *Writer) writeSync(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	wt, end := w.enqueue(p, true)
	return w.result(wt, end)
}

// enqueue queues p. With wait, it registers and returns a waiter for p, on
// which the caller must then call result, and p's end offset.
func (w *Writer) enqueue(p []byte, wait bool) (*waiter, uint64) {
	w.mu.Lock()
	for (len(w.pending) > 0 || w.inflight > 0) && len(w.pending)+w.inflight+len(p) > w.limit {
		w.cond.Wait()
	}
	wasEmpty := len(w.pending) == 0
	var wt *waiter
	if wait {
		wt = w.registerLocked()
	}
	w.pending = append(w.pending, p...)
	w.queued += uint64(len(p))
	end := w.queued
	w.mu.Unlock()
	if wasEmpty {
		// The drainer empties the queue before it waits again, so a kick is
		// needed only when the queue goes from empty to non-empty.
		select {
		case w.kick <- struct{}{}:
		default:
		}
	}
	return wt, end
}

func (w *Writer) registerLocked() *waiter {
	wt := &waiter{start: w.queued}
	w.waiters[wt] = struct{}{}
	return wt
}

// result waits until the bytes from wt's start to end are done, unregisters
// wt, and reports how many were written and the destination's error, if
// any, for them.
func (w *Writer) result(wt *waiter, end uint64) (int, error) {
	w.waitThrough(end)
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.waiters, wt)
	start := wt.start
	if f := wt.first; f != nil && f.start < end && (f.through < end || f.through == f.end) {
		return int(min(max(f.through, start), end) - start), f.err
	}
	return int(end - start), nil
}

// waitThrough waits until every byte before stream offset end is done.
func (w *Writer) waitThrough(end uint64) {
	for {
		w.mu.Lock()
		if w.written >= end {
			w.mu.Unlock()
			return
		}
		var ch chan struct{}
		if end <= w.written+uint64(w.inflight) {
			if w.inflightBatch == nil {
				w.inflightBatch = make(chan struct{})
			}
			ch = w.inflightBatch
		} else {
			if w.pendingBatch == nil {
				w.pendingBatch = make(chan struct{})
			}
			ch = w.pendingBatch
		}
		w.mu.Unlock()
		<-ch
	}
}

// Flush waits until everything queued before the call is done, and returns
// a destination error from any of it not yet written when Flush was called.
func (w *Writer) Flush() error {
	w.mu.Lock()
	wt := &waiter{start: w.written}
	w.waiters[wt] = struct{}{}
	end := w.queued
	w.mu.Unlock()
	_, err := w.result(wt, end)
	return err
}

func (w *Writer) drain() {
	for range w.kick {
		w.mu.Lock()
		for len(w.pending) > 0 {
			buf := w.pending
			w.pending, w.spare = w.spare[:0], nil
			w.inflight, w.inflightBatch, w.pendingBatch = len(buf), w.pendingBatch, nil
			start := w.written
			w.mu.Unlock()
			n, err := writeAll(w.out, buf)
			w.mu.Lock()
			if err != nil {
				f := &failure{start: start, end: start + uint64(len(buf)), through: start + uint64(n), err: err}
				for wt := range w.waiters {
					if wt.first == nil && f.end > wt.start {
						wt.first = f
					}
				}
			}
			w.written += uint64(len(buf))
			w.inflight = 0
			if w.inflightBatch != nil {
				close(w.inflightBatch)
				w.inflightBatch = nil
			}
			if cap(buf) <= 2*w.limit {
				w.spare = buf[:0]
			}
			w.cond.Broadcast()
		}
		w.mu.Unlock()
	}
}

// writeAll writes buf, continuing after short writes, until it is written or
// the destination fails, and returns how many bytes were written.
func writeAll(out io.Writer, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := out.Write(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

// isWarnOrWorse reports whether a formatted line is at WARN level or worse,
// from the level field slog's TextHandler and logrus's TextFormatter put
// near its start.
func isWarnOrWorse(p []byte) bool {
	i := bytes.Index(p[:min(len(p), 96)], []byte(" level="))
	if i < 0 {
		return false
	}
	lv := p[i+len(" level="):]
	for _, s := range [][]byte{[]byte("WARN"), []byte("ERROR"), []byte("warning"), []byte("error"), []byte("fatal"), []byte("panic")} {
		if bytes.HasPrefix(lv, s) {
			return true
		}
	}
	return false
}
