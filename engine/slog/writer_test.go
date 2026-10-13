package slog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recorder is a destination that records each write and can be slowed down.
type recorder struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	writes  int
	delay   time.Duration
	inside  atomic.Int32
	overlap atomic.Bool
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.inside.Add(1) > 1 {
		r.overlap.Store(true)
	}
	defer r.inside.Add(-1)
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes++
	return r.buf.Write(p)
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

// syncWriter makes every Write wait until its bytes are written, as WARN
// and worse lines do.
type syncWriter struct{ *Writer }

func (s syncWriter) Write(p []byte) (int, error) { return s.writeSync(p) }

func checkLines(t *testing.T, out string, goroutines, perG int) {
	t.Helper()
	next := make([]int, goroutines)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != goroutines*perG {
		t.Fatalf("got %d lines, want %d", len(lines), goroutines*perG)
	}
	for _, l := range lines {
		var g, i int
		if _, err := fmt.Sscanf(l, "g=%d i=%d end", &g, &i); err != nil {
			t.Fatalf("torn or malformed line %q: %v", l, err)
		}
		if i != next[g] {
			t.Fatalf("goroutine %d: line %d out of order, want %d", g, i, next[g])
		}
		next[g]++
	}
}

func runWriters(w io.Writer, goroutines, perG int) {
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perG {
				fmt.Fprintf(w, "g=%d i=%d end\n", g, i)
			}
		}()
	}
	wg.Wait()
}

func TestWriterOrderAndIntegrity(t *testing.T) {
	for _, sync := range []bool{false, true} {
		t.Run(fmt.Sprintf("sync=%v", sync), func(t *testing.T) {
			rec := &recorder{delay: 20 * time.Microsecond}
			w := NewWriter(rec, 4096)
			var out io.Writer = w
			if sync {
				out = syncWriter{w}
			}
			runWriters(out, 16, 500)
			w.Flush()
			checkLines(t, rec.String(), 16, 500)
			if rec.overlap.Load() {
				t.Fatal("destination written concurrently")
			}
			if rec.writes >= 16*500 {
				t.Fatalf("no batching: %d writes for %d lines", rec.writes, 16*500)
			}
		})
	}
}

// A synchronous write returns only after its bytes reached the destination.
func TestWriterSyncWrittenOnReturn(t *testing.T) {
	rec := &recorder{delay: 50 * time.Microsecond}
	w := syncWriter{NewWriter(rec, 1<<20)}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				line := fmt.Sprintf("g=%d i=%d end\n", g, i)
				w.Write([]byte(line))
				if !strings.Contains(rec.String(), line) {
					t.Errorf("line %q not written when Write returned", line)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// A slow destination blocks writers at the limit instead of growing the
// queue or dropping output.
func TestWriterAsyncBackpressure(t *testing.T) {
	rec := &recorder{delay: 2 * time.Millisecond}
	const limit = 1024
	w := NewWriter(rec, limit)
	var maxPending atomic.Int64
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			w.mu.Lock()
			if n := int64(len(w.pending)); n > maxPending.Load() {
				maxPending.Store(n)
			}
			w.mu.Unlock()
			time.Sleep(50 * time.Microsecond)
		}
	}()
	runWriters(w, 8, 100)
	w.Flush()
	close(stop)
	checkLines(t, rec.String(), 8, 100)
	if m := maxPending.Load(); m > limit+64 {
		t.Fatalf("queue grew to %d bytes, limit %d", m, limit)
	}
}

// Flush returns only once everything written before it is out.
func TestWriterAsyncFlush(t *testing.T) {
	rec := &recorder{delay: time.Millisecond}
	w := NewWriter(rec, 1<<20)
	runWriters(w, 4, 50)
	w.Flush()
	checkLines(t, rec.String(), 4, 50)
}

// With SyncWarn, WARN and worse lines are written when Write returns; other
// lines may still be queued.
func TestWriterSyncWarn(t *testing.T) {
	rec := &recorder{delay: 2 * time.Millisecond}
	w := NewWriter(rec, 1<<20)
	w.SyncWarn = true
	lg := slog.New(slog.NewTextHandler(w, nil))
	lgr := logrusLine("warning")
	for i := range 20 {
		lg.Info("info line", "i", i)
		lg.Warn("warn line", "i", i)
		if !strings.Contains(rec.String(), fmt.Sprintf("msg=\"warn line\" i=%d\n", i)) {
			t.Fatalf("warn line %d not written on return", i)
		}
		w.Write(lgr)
		if !strings.Contains(rec.String(), string(lgr)) {
			t.Fatal("logrus warning not written on return")
		}
	}
	if !isWarnOrWorse([]byte("time=2026-10-10T00:00:00.000Z level=ERROR msg=x\n")) ||
		isWarnOrWorse([]byte("time=2026-10-10T00:00:00.000Z level=INFO msg=\"level=WARN\"\n")) {
		t.Fatal("level detection")
	}
}

func logrusLine(level string) []byte {
	return []byte("time=\"2026-10-10T00:00:00Z\" level=" + level + " msg=\"x\"\n")
}

// Empty writes return at once, including synchronous ones.
func TestWriterEmptyWrite(t *testing.T) {
	rec := &recorder{}
	w := NewWriter(rec, 1<<20)
	done := make(chan struct{})
	go func() {
		w.writeSync(nil)
		syncWriter{w}.Write([]byte{})
		w.Write(nil)
		w.Flush()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("empty write did not return")
	}
}

// failing accepts at most n bytes per call, then fails every call once
// failAfter bytes have been accepted.
type failing struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	n         int
	failAfter int
}

var errSink = errors.New("sink failed")

func (f *failing) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buf.Len() >= f.failAfter {
		return 0, errSink
	}
	p = p[:min(len(p), f.n)]
	return f.buf.Write(p)
}

// Short writes are continued; a destination error reaches the callers
// waiting on those bytes, with the count of their bytes that were written.
func TestWriterShortWritesAndErrors(t *testing.T) {
	f := &failing{n: 3, failAfter: 1 << 30}
	w := NewWriter(f, 1<<20)
	if n, err := w.writeSync([]byte("hello world\n")); err != nil || n != 12 {
		t.Fatalf("WriteSync = %d, %v", n, err)
	}
	if got := f.buf.String(); got != "hello world\n" {
		t.Fatalf("short writes not continued: %q", got)
	}
	// The destination accepts 3 more bytes, then fails.
	f.failAfter = f.buf.Len() + 3
	if n, err := (syncWriter{w}).Write([]byte("abcdef\n")); !errors.Is(err, errSink) || n != 3 {
		t.Fatalf("syncWriter.Write = %d, %v; want 3, %v", n, err, errSink)
	}
	if n, err := w.writeSync([]byte("lost\n")); !errors.Is(err, errSink) || n != 0 {
		t.Fatalf("WriteSync = %d, %v; want 0, %v", n, err, errSink)
	}
}

// gated blocks every write until released.
type gated struct {
	recorder
	gate chan struct{}
}

func (g *gated) Write(p []byte) (int, error) {
	<-g.gate
	return g.recorder.Write(p)
}

// The limit covers the batch being written too, and a line larger than the
// limit goes through alone.
func TestWriterLimitIncludesInflight(t *testing.T) {
	g := &gated{gate: make(chan struct{})}
	const limit = 16
	w := NewWriter(g, limit)
	big := bytes.Repeat([]byte("x"), 100)
	w.Write(big) // oversized, queue empty: accepted, then held by the gate
	time.Sleep(20 * time.Millisecond)
	second := make(chan struct{})
	go func() {
		w.Write([]byte("small\n"))
		close(second)
	}()
	select {
	case <-second:
		t.Fatal("write accepted while an oversized batch was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(g.gate)
	<-second
	w.Flush()
	if got := g.String(); got != string(big)+"small\n" {
		t.Fatalf("got %q", got)
	}
}

// Empty writes return at once even while the destination is blocked.
func TestWriterEmptyWhileBlocked(t *testing.T) {
	g := &gated{gate: make(chan struct{})}
	w := NewWriter(g, 1<<20)
	w.Write([]byte("held\n"))
	done := make(chan struct{})
	go func() {
		w.writeSync(nil)
		syncWriter{w}.Write([]byte{})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("empty write waited for unrelated output")
	}
	close(g.gate)
}

// fullCountErr takes every byte but reports an error.
type fullCountErr struct{}

func (fullCountErr) Write(p []byte) (int, error) { return len(p), errSink }

// An error reported with a full count still reaches the waiter.
func TestWriterFullCountError(t *testing.T) {
	w := NewWriter(fullCountErr{}, 1<<20)
	if n, err := w.writeSync([]byte("ab")); n != 2 || !errors.Is(err, errSink) {
		t.Fatalf("WriteSync = %d, %v; want 2, %v", n, err, errSink)
	}
}

// Waiters are released once they have their result, so failures are not
// retained for them, however many occur.
func TestWriterWaitersReleased(t *testing.T) {
	w := NewWriter(&failing{n: 1, failAfter: 0}, 16)
	for range 2000 {
		if _, err := w.writeSync([]byte("ab")); !errors.Is(err, errSink) {
			t.Fatalf("writeSync error = %v", err)
		}
		w.Write([]byte("cd"))
		w.Flush()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if n := len(w.waiters); n != 0 {
		t.Fatalf("%d waiters still registered", n)
	}
}

// WARN and worse lines written through the public Write get the
// destination's error and the count of their bytes that were written.
func TestWriterWarnErrors(t *testing.T) {
	for _, level := range []string{"WARN", "warning", "ERROR"} {
		t.Run(level, func(t *testing.T) {
			p := logrusLine(level)
			w := NewWriter(&failing{n: 3, failAfter: 3}, 1<<20)
			w.SyncWarn = true
			if n, err := w.Write(p); n != 3 || !errors.Is(err, errSink) {
				t.Fatalf("partial Write = %d, %v; want 3, %v", n, err, errSink)
			}
			w = NewWriter(fullCountErr{}, 1<<20)
			w.SyncWarn = true
			if n, err := w.Write(p); n != len(p) || !errors.Is(err, errSink) {
				t.Fatalf("full-count Write = %d, %v; want %d, %v", n, err, len(p), errSink)
			}
		})
	}
}

// gatedFailing blocks every write until released, then fails it.
type gatedFailing struct {
	gate, entered chan struct{}
	once          sync.Once
}

func (g *gatedFailing) Write(p []byte) (int, error) {
	g.once.Do(func() { close(g.entered) })
	<-g.gate
	return 0, errSink
}

func waitForWaiters(t *testing.T, w *Writer, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		w.mu.Lock()
		n := len(w.waiters)
		w.mu.Unlock()
		if n == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d waiters, want %d", n, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// A Flush and many WARN lines waiting on failing batches all get the error,
// and none of them stays registered.
func TestWriterFlushAndWarnErrorFanout(t *testing.T) {
	sink := &gatedFailing{gate: make(chan struct{}), entered: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(sink.gate) }) }
	defer release()
	w := NewWriter(sink, 1<<20)
	w.SyncWarn = true
	w.Write(logrusLine("info"))
	<-sink.entered
	const calls = 64
	results := make(chan error, calls+1)
	go func() { results <- w.Flush() }()
	waitForWaiters(t, w, 1)
	for range calls {
		go func() {
			n, err := w.Write(logrusLine("warning"))
			if n != 0 {
				t.Errorf("count = %d, want 0", n)
			}
			results <- err
		}()
	}
	waitForWaiters(t, w, calls+1)
	release()
	for range calls + 1 {
		if err := <-results; !errors.Is(err, errSink) {
			t.Errorf("result = %v, want %v", err, errSink)
		}
	}
	waitForWaiters(t, w, 0)
	if err := w.Flush(); err != nil {
		t.Errorf("Flush after completion = %v, want nil", err)
	}
}
