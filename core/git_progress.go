package core

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strconv"
	"sync"
	"time"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/util/gitutil"
)

// gitReceivingObjectsRE matches git's sideband transfer progress, e.g.
// "Receiving objects:  42% (1234/2900), 5.6 MiB | 2.3 MiB/s".
var gitReceivingObjectsRE = regexp.MustCompile(`Receiving objects:\s+\d+% \((\d+)/(\d+)\)`)

// gitFetchProgressStreams returns a gitutil.StreamFunc that parses `git
// fetch --progress` stderr and streams the received-object counts via the
// telemetry convention, attributed to the span carried by ctx (the
// "fetching <remote>" span rather than the per-command span, so a named-ref
// retry continues the same bar).
func gitFetchProgressStreams(ctx context.Context) gitutil.StreamFunc {
	return func(context.Context) (io.WriteCloser, io.WriteCloser, func()) {
		return nopWriteCloser{io.Discard}, &gitProgressWriter{ctx: ctx}, func() {}
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

type gitProgressWriter struct {
	ctx context.Context

	mu       sync.Mutex
	buf      bytes.Buffer
	current  int64
	total    int64
	lastEmit time.Time
}

func (w *gitProgressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	// live progress updates are separated by \r, final ones by \n
	for {
		i := bytes.IndexAny(w.buf.Bytes(), "\r\n")
		if i < 0 {
			break
		}
		line := string(w.buf.Next(i + 1))
		m := gitReceivingObjectsRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		w.current, _ = strconv.ParseInt(m[1], 10, 64)
		w.total, _ = strconv.ParseInt(m[2], 10, 64)
		// purely throttled: Close emits the final parsed state
		if now := time.Now(); now.Sub(w.lastEmit) >= bkcache.ProgressEmitInterval {
			w.lastEmit = now
			bkcache.EmitProgress(w.ctx, "objects", w.current, w.total, "objects")
		}
	}
	return len(p), nil
}

func (w *gitProgressWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.total > 0 {
		bkcache.EmitProgress(w.ctx, "objects", w.current, w.total, "objects")
	}
	return nil
}
