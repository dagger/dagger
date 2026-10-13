package slog

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
)

// BenchmarkLogToPipe logs one "start call"-shaped line per op from parallel
// goroutines through a TextHandler into a pipe drained by a reader, as the
// engine's stderr is.
func BenchmarkLogToPipe(b *testing.B) {
	for _, mode := range []string{"direct", "syncwarn", "async"} {
		b.Run(mode, func(b *testing.B) {
			r, wf, err := os.Pipe()
			if err != nil {
				b.Fatal(err)
			}
			done := make(chan struct{})
			go func() { io.Copy(io.Discard, r); close(done) }()
			var w *Writer
			var h slog.Handler = slog.NewTextHandler(wf, &slog.HandlerOptions{Level: slog.LevelDebug})
			if mode != "direct" {
				w = NewWriter(wf, 1<<20)
				w.SyncWarn = mode == "syncwarn"
				h = slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
			}
			lg := slog.New(h).With(
				"client_id", "kz3x0tv6mxq5c1w2lfkqk3tm2", "client_hostname", "f2a1c3b4d5e6",
				"session_id", "m8ycr0s7rwq3a9b4f6c2e1d0k", "trace", "4bf92f3577b34da6a3ce929d0e0e4736",
				"span", "00f067aa0ba902b7")
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					lg.InfoContext(ctx, "start call", "field", "Container.withExec",
						"digest", "xxh3:3c2a8d1f0b9e7a65")
				}
			})
			b.StopTimer()
			if w != nil {
				w.Flush()
			}
			wf.Close()
			<-done
		})
	}
}
