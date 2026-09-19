package core

import (
	"bytes"
	"context"
	"io"
	"math"
	"regexp"
	"strconv"
	"sync"
	"time"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	enginetelemetry "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/util/gitutil"
)

// gitReceivingObjectsRE matches git's sideband transfer progress, e.g.
// "Receiving objects:  42% (1234/2900), 5.6 MiB | 2.3 MiB/s".
var gitReceivingObjectsRE = regexp.MustCompile(`Receiving objects:\s+\d+% \((\d+)/(\d+)\)(?:,\s+([0-9]+(?:\.[0-9]+)?)\s+(bytes|[KMGT]?i?B))?`)

// gitFetchProgressStreams returns a gitutil.StreamFunc that parses `git
// fetch --progress` stderr and streams the transferred pack size when Git
// reports it, falling back to received-object counts. Progress is attributed
// to the span carried by ctx (the "fetching <remote>" span rather than the
// per-command span, so a named-ref retry continues the same bar).
func gitFetchProgressStreams(ctx context.Context) gitutil.StreamFunc {
	network, _ := enginetelemetry.NewNetworkAccumulator(ctx, enginetelemetry.NetworkRX)
	return func(context.Context) (io.WriteCloser, io.WriteCloser, func()) {
		return nopWriteCloser{io.Discard}, &gitProgressWriter{ctx: ctx, network: network}, func() {}
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
	bytes    int64
	hasBytes bool
	network  *enginetelemetry.NetworkAccumulator
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
		if len(m) >= 5 && m[3] != "" {
			if byteCount, ok := parseGitByteSize(m[3], m[4]); ok {
				if byteCount > w.bytes {
					w.network.Add(byteCount - w.bytes)
				}
				w.bytes = byteCount
				w.hasBytes = true
			}
		}
		// purely throttled: Close emits the final parsed state
		if now := time.Now(); now.Sub(w.lastEmit) >= bkcache.ProgressEmitInterval {
			w.lastEmit = now
			w.emit(false)
		}
	}
	return len(p), nil
}

func (w *gitProgressWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.hasBytes {
		w.emit(true)
	} else if w.total > 0 {
		bkcache.EmitProgress(w.ctx, "git fetch", w.current, w.total, "objects")
	}
	return nil
}

func (w *gitProgressWriter) emit(final bool) {
	if w.hasBytes {
		var total int64
		if final {
			total = w.bytes
		}
		bkcache.EmitProgress(w.ctx, "git fetch", w.bytes, total, "bytes")
		return
	}
	if w.total > 0 {
		bkcache.EmitProgress(w.ctx, "git fetch", w.current, w.total, "objects")
	}
}

func parseGitByteSize(value, unit string) (int64, bool) {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	multiplier := float64(1)
	switch unit {
	case "B", "bytes":
	case "KB":
		multiplier = 1000
	case "KiB":
		multiplier = 1 << 10
	case "MB":
		multiplier = 1000 * 1000
	case "MiB":
		multiplier = 1 << 20
	case "GB":
		multiplier = 1000 * 1000 * 1000
	case "GiB":
		multiplier = 1 << 30
	case "TB":
		multiplier = 1000 * 1000 * 1000 * 1000
	case "TiB":
		multiplier = 1 << 40
	default:
		return 0, false
	}
	return int64(math.Round(n * multiplier)), true
}
