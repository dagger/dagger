package foldreplay

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dagger/dagger/internal/spanfootprint"
)

var (
	replayPath  = flag.String("foldreplay.replay", os.Getenv("DAGGER_FOLD_REPLAY"), "capture to replay (BenchmarkFoldReplay)")
	replayRoot  = flag.String("foldreplay.root", os.Getenv("DAGGER_FOLD_REPLAY_ROOT"), "span ID whose subtree to replay (default: the whole trace)")
	replayModes = flag.String("foldreplay.modes", os.Getenv("DAGGER_FOLD_REPLAY_MODES"), "comma-separated DBs to measure: full, fold, fold-nocalls (default full,fold)")

	replayRowWaits    = flag.Int("foldreplay.rowwaits", 0, "compute row waits (UpdateRowWaits): every N exports during the replay and at the end; -1 only at the end; 0 never")
	replayRowWaitsBdg = flag.Duration("foldreplay.rowwaits.budget", 20*time.Millisecond, "budget of each UpdateRowWaits during the replay (the live CLI's slice)")
	replayHeapProfile = flag.String("foldreplay.heapprofile", "", "directory to write a heap profile per mode to, heap-<mode>.pprof")
	replayBreakdown   = flag.Bool("foldreplay.breakdown", false, "log Fold.Breakdown for folding modes")
	replayAnatomy     = flag.Bool("foldreplay.anatomy", false, "log Fold.CauseAnatomy and Fold.WaitAnatomy for folding modes")

	capturePath  = flag.String("foldreplay.capture", os.Getenv("DAGGER_FOLD_CAPTURE"), "capture to record (TestFoldCapture), a .otlp path")
	captureTrace = flag.String("foldreplay.trace", os.Getenv("DAGGER_FOLD_CAPTURE_TRACE"), "Dagger Cloud trace ID to record")
	captureCmd   = flag.String("foldreplay.cmd", os.Getenv("DAGGER_FOLD_CAPTURE_CMD"), "dagger command to record: the binary and its arguments, space-separated")
	captureDir   = flag.String("foldreplay.dir", os.Getenv("DAGGER_FOLD_CAPTURE_DIR"), "directory to run the dagger command in")
)

// BenchmarkFoldReplay replays a capture (see the package doc) into a DB as
// the live CLI builds it (dagui.NewDB) and folding with its policy, and
// reports the heap each holds per span and, folding, the share of spans it
// retains:
//
//	go test ./internal/foldreplay -run '^$' -bench FoldReplay -benchtime 1x -foldreplay.replay trace.otlp
//
// -foldreplay.root <span ID> replays only that span's subtree, and
// -foldreplay.modes fold leaves out the full load, which a trace of
// millions of spans may not fit in memory for. Each of these flags has an
// environment variable too (DAGGER_FOLD_REPLAY, _ROOT, _MODES).
//
// -foldreplay.rowwaits N computes the waiting below rows as the live CLI
// does, -foldreplay.heapprofile <dir> writes a heap profile per mode (slice
// it with `go tool pprof -top`), and -foldreplay.breakdown logs what a fold
// retains and why (Fold.Breakdown). TestFoldCapture records a capture.
func BenchmarkFoldReplay(b *testing.B) {
	if *replayPath == "" {
		b.Skip("no capture to replay: -foldreplay.replay or DAGGER_FOLD_REPLAY")
	}
	tr, err := Open(*replayPath, *replayRoot)
	require.NoError(b, err)
	defer tr.Close()
	if *replayHeapProfile != "" {
		require.NoError(b, os.MkdirAll(*replayHeapProfile, 0o755))
	}
	modes := strings.Split(*replayModes, ",")
	if modes[0] == "" {
		modes = []string{"full", "fold"}
	}
	for _, mode := range modes {
		b.Run(mode, func(b *testing.B) {
			opts := Options{
				Fold:           strings.HasPrefix(mode, "fold"),
				NoFoldedCalls:  mode == "fold-nocalls",
				RowWaits:       *replayRowWaits,
				RowWaitsBudget: *replayRowWaitsBdg,
				Breakdown:      *replayBreakdown,
				Anatomy:        *replayAnatomy,
			}
			if *replayHeapProfile != "" {
				opts.HeapProfile = filepath.Join(*replayHeapProfile, "heap-"+mode+".pprof")
			}
			var res Result
			for b.Loop() {
				res, err = Measure(b.Context(), tr, opts)
				require.NoError(b, err)
			}
			b.ReportMetric(float64(res.Spans), "spans")
			b.ReportMetric(res.BytesPerSpan, "B/span")
			b.ReportMetric(float64(res.Bytes)/1e9, "GB")
			if opts.RowWaits != 0 {
				b.ReportMetric(float64(res.RowWaitsBytes)/1e9, "GB-rowwaits-settle")
			}
			if opts.Fold {
				b.ReportMetric(100*res.Retained, "%retained")
				b.ReportMetric(float64(res.Loads), "loads")
				b.ReportMetric(float64(res.Stats.StubBytes)/1e9, "GB-stubs")
			}
			if res.Breakdown != nil {
				b.Logf("%s breakdown:\n%s", mode, FormatBreakdown(*res.Breakdown))
			}
			if res.CauseAnatomy != nil || res.WaitAnatomy != nil {
				b.Logf("%s cause anatomy:\n%s\n%s wait anatomy:\n%s\n%s link rule what-ifs (retained spans):\n%s", mode, FormatCounts(res.CauseAnatomy),
					mode, FormatCounts(res.WaitAnatomy), mode, FormatCounts(res.LinkWhatIfs))
			}
		})
	}
}

// TestFoldCapture records a capture for BenchmarkFoldReplay, either of a
// Dagger Cloud trace (with DAGGER_CLOUD_TOKEN or `dagger login`):
//
//	go test ./internal/foldreplay -run TestFoldCapture -foldreplay.capture trace.otlp -foldreplay.trace <trace ID>
//
// or of a dagger command run now, whose CLI forwards every span of its
// session, the engine's included, to the capture:
//
//	go test ./internal/foldreplay -run TestFoldCapture -foldreplay.capture run.otlp -foldreplay.cmd "dagger call engine-dev test --run TestFoo"
//
// Each flag has an environment variable too (DAGGER_FOLD_CAPTURE, _TRACE,
// _CMD, _DIR).
func TestFoldCapture(t *testing.T) {
	if *capturePath == "" {
		t.Skip("no capture to record: -foldreplay.capture or DAGGER_FOLD_CAPTURE")
	}
	c, err := Create(*capturePath)
	require.NoError(t, err)
	if *captureTrace != "" {
		require.NoError(t, FetchCloud(t.Context(), *captureTrace, c))
		require.NoError(t, c.Close())
		return
	}
	args := strings.Fields(*captureCmd)
	require.NotEmpty(t, args, "set -foldreplay.trace or -foldreplay.cmd")
	endpoint, stop, err := Serve(c)
	require.NoError(t, err)
	cmd := exec.CommandContext(t.Context(), args[0], args[1:]...)
	cmd.Dir = *captureDir
	cmd.Env = append(os.Environ(),
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="+endpoint+"/v1/traces",
		"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="+endpoint+"/v1/logs",
		"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/protobuf",
	)
	out, runErr := cmd.CombinedOutput()
	require.NoError(t, stop())
	require.NoError(t, c.Close())
	if runErr != nil {
		// a failing run's trace is a trace all the same
		t.Logf("%s: %v\n%s", strings.Join(args, " "), runErr, tail(string(out), 4000))
	}
	tr, err := Open(*capturePath, "")
	require.NoError(t, err)
	defer tr.Close()
	t.Logf("captured %d spans", tr.Len())
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// A capture of the synthetic agent session, in either format, replays into
// a full and a folding DB.
func TestReplay(t *testing.T) {
	const n = 5000
	sf := spanfootprint.New(n)
	dir := t.TempDir()
	otlp := filepath.Join(dir, "trace.otlp")
	c, err := Create(otlp)
	require.NoError(t, err)
	jsonl, err := os.Create(filepath.Join(dir, "trace.jsonl"))
	require.NoError(t, err)
	require.NoError(t, sf.Export(t.Context(), captureExporter(func(req *coltracepb.ExportTraceServiceRequest) error {
		line, err := protojson.Marshal(req)
		if err != nil {
			return err
		}
		if _, err := jsonl.Write(append(line, '\n')); err != nil {
			return err
		}
		return c.Write(req)
	}), 512, nil))
	require.NoError(t, c.Close())
	require.NoError(t, jsonl.Close())

	for _, path := range []string{otlp, jsonl.Name()} {
		t.Run(filepath.Ext(path), func(t *testing.T) {
			tr, err := Open(path, "")
			require.NoError(t, err)
			defer tr.Close()
			require.Equal(t, n, tr.Len())
			full, err := Measure(t.Context(), tr, Options{RowWaits: 4})
			require.NoError(t, err)
			folded, err := Measure(t.Context(), tr, Options{Fold: true, RowWaits: 4, Breakdown: true, Anatomy: true,
				HeapProfile: filepath.Join(t.TempDir(), "heap.pprof")})
			require.NoError(t, err)
			require.NotNil(t, folded.Breakdown)
			require.Equal(t, n, folded.Breakdown.Spans)
			require.NotEmpty(t, FormatBreakdown(*folded.Breakdown))
			require.Equal(t, n, full.Spans)
			require.Greater(t, folded.Retained, 0.0)
			require.Less(t, folded.Retained, 1.0)
			require.Less(t, folded.BytesPerSpan, full.BytesPerSpan)
		})
	}

	// a subtree: an agent loop's
	tr, err := Open(otlp, sf.SpanID(1).String())
	require.NoError(t, err)
	defer tr.Close()
	require.Greater(t, tr.Len(), 1)
	require.Less(t, tr.Len(), n)
}

type captureExporter func(*coltracepb.ExportTraceServiceRequest) error

func (e captureExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	return e(&coltracepb.ExportTraceServiceRequest{ResourceSpans: telemetry.SpansToPB(spans)})
}

func (captureExporter) Shutdown(context.Context) error { return nil }
