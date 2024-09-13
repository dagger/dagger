package idtui_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/koron-go/prefixw"
	"github.com/stretchr/testify/require"
	"github.com/vito/midterm"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
	"gotest.tools/v3/golden"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/testctx"
)

var testCtx = context.Background()

func TestMain(m *testing.M) {
	testCtx = telemetry.InitEmbedded(testCtx, nil)
	code := m.Run()
	telemetry.Close()
	os.Exit(code) // don't use defer!
}

const InstrumentationLibrary = "dagger.io/golden"

func Tracer() trace.Tracer {
	return otel.Tracer(InstrumentationLibrary)
}

func Logger() log.Logger {
	return telemetry.Logger(testCtx, InstrumentationLibrary)
}

func Middleware() []testctx.Middleware {
	return []testctx.Middleware{
		testctx.WithParallel,
		testctx.WithOTelLogging(Logger()),
		testctx.WithOTelTracing(Tracer()),
	}
}

type TelemetrySuite struct {
	Home string
}

func TestTelemetry(t *testing.T) {
	testctx.Run(testCtx, t, TelemetrySuite{
		Home: t.TempDir(),
	}, Middleware()...)
}

func (s TelemetrySuite) TestGolden(ctx context.Context, t *testctx.T) {
	for _, ex := range []Example{
		{Function: "hello-world"},
		{Function: "fail-log", Fail: true},
		{Function: "fail-effect", Fail: true},
		{Function: "encapsulate"},
		{Function: "pending", Fail: true},
		{Function: "use-exec-service"},
		{Function: "use-no-exec-service"},
		{Module: "./viztest/broken", Function: "broken", Fail: true},
	} {
		t.Run(ex.Function, func(ctx context.Context, t *testctx.T) {
			out, _ := ex.Run(ctx, t, s)
			golden.Assert(t, out, t.Name())
		})
	}
}

type Example struct {
	Module   string
	Function string
	Args     []string
	Fail     bool
}

func (ex Example) Run(ctx context.Context, t *testctx.T, s TelemetrySuite) (string, *dagui.DB) {
	db, otlpL := testDB(t)

	if ex.Module == "" {
		ex.Module = "./viztest"
	}

	daggerArgs := []string{"--progress=report", "call", "-m", ex.Module, ex.Function}
	daggerArgs = append(daggerArgs, ex.Args...)

	// NOTE: we care about CACHED states for these tests, so we need some way for
	// them to not be flaky (cache hit/miss), but still produce the same golden
	// output every time. So, we run everything twice. The first run will cache
	// the things that should be cacheable, and the second run will be the final
	// result. Each test is responsible for busting its own caches.
	func() {
		ctx, span := Tracer().Start(ctx, "warmup")
		defer telemetry.End(span, func() error { return nil })
		warmup := exec.Command("dagger", daggerArgs...)
		warmup.Env = append(
			os.Environ(),
			fmt.Sprintf("HOME=%s", s.Home), // ignore any local Dagger Cloud auth
		)
		warmup.Env = append(warmup.Env, telemetry.PropagationEnv(ctx)...)
		err := warmup.Run()
		if ex.Fail {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
		time.Sleep(10 * time.Second)
	}()

	cmd := exec.Command("dagger", daggerArgs...)
	cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("HOME=%s", s.Home), // ignore any local Dagger Cloud auth
		"NO_COLOR=1",
		"OTEL_EXPORTER_OTLP_TRACES_LIVE=1",
		fmt.Sprintf("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://%s/v1/traces", otlpL.Addr().String()),
		fmt.Sprintf("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://%s/v1/logs", otlpL.Addr().String()),
	)
	cmd.Env = append(cmd.Env, telemetry.PropagationEnv(ctx)...)

	errBuf := new(bytes.Buffer)
	cmd.Stderr = io.MultiWriter(errBuf, prefixw.New(testutil.NewTWriter(t), "stderr: "))
	cmd.Stdout = prefixw.New(testutil.NewTWriter(t), "stdout: ")

	err := cmd.Run()
	if ex.Fail {
		require.Error(t, err)
	} else {
		require.NoError(t, err)
	}

	return stabilize(errBuf.String()), db
}

type scrubber struct {
	re     *regexp.Regexp
	sample string
	repl   string
}

const privateIP = `10\.\d+\.\d+\.\d+`
const month = `Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec`

var scrubs = []scrubber{
	// Redis logs
	{
		regexp.MustCompile(`\d+:([MC]) \d+ (` + month + `) 20\d+ \d+:\d+:\d+\.\d+`),
		"7:C 1 Jan 2020 00:00:00.000",
		"X:M XX XXX 20XX XX:XX:XX.XXX",
	},
	{
		regexp.MustCompile(`\bpid=\d+\b`),
		"pid=8",
		"pid=X",
	},
	// Durations
	{
		regexp.MustCompile(`\b(\d+m)?\d+\.\d+s\b`),
		"1m2.345s",
		"X.Xs",
	},
	// IP addresses
	{
		regexp.MustCompile(`\[::ffff:` + privateIP + `\]:\d+:`),
		"[::ffff:10.89.0.8]:53172:",
		"[::ffff:10.XX.XX.XX]:XXXXX:",
	},
	{
		regexp.MustCompile(`\b` + privateIP + `\b`),
		"10.89.0.8",
		"10.XX.XX.XX",
	},
	// time.Now().String()
	{
		regexp.MustCompile(`\b20\d{2}-\d{2}-\d{2} \d+:\d+:\d+\.\d+ \+\d{4} UTC m=\+\d+.\d+\b`),
		"2024-09-12 10:02:03.4567 +0000 UTC m=+0.987654321",
		"20XX-XX-XX XX:XX:XX.XXXX +XXXX UTC m=+X.X",
	},
	// Dates
	{
		regexp.MustCompile(`\b20\d{2}-\d{2}-\d{2}\b`),
		"2024-09-12",
		"20XX-XX-XX",
	},
	{
		regexp.MustCompile(`\b\d+/(` + month + `)/20\d{2}\b`),
		"12/Jan/2024",
		"XX/XXX/20XX",
	},
	// Times
	{
		regexp.MustCompile(`\b\d+:\d+:\d+\b`),
		"12:34:56",
		"XX:XX:XX",
	},
	// Trailing whitespace from logs (ignores NO_COLOR - bug)
	{
		regexp.MustCompile(`\s*` + regexp.QuoteMeta(midterm.Reset.Render())),
		"	        \x1b[0m",
		"",
	},
	// Dagger Cloud logged out
	{
		regexp.MustCompile(`\b` + strings.Join(idtui.SkipLoggedOutTraceMsgEnvs, "|") + `\b`),
		"SHUTUP",
		"NOTHANKS",
	},
}

func TestScrubbers(t *testing.T) {
	// quick sanity check to make sure the regexes work, since regexes are hard
	for _, s := range scrubs {
		require.Regexp(t, s.re, s.sample)
	}
}

func stabilize(out string) string {
	for _, s := range scrubs {
		out = s.re.ReplaceAllString(out, s.repl)
	}
	return out
}

func testDB(t *testctx.T) (*dagui.DB, net.Listener) {
	db := dagui.NewDB()
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	srv := newReceiver(t, db, db.LogExporter())
	go http.Serve(l, srv)

	return db, l
}

func newReceiver(t *testctx.T, traces sdktrace.SpanExporter, logs sdklog.Exporter) http.Handler {
	mux := http.NewServeMux()
	ps := &otlpReceiver{
		t:      t,
		traces: traces,
		logs:   logs,
	}
	mux.HandleFunc("POST /v1/traces", ps.TracesHandler)
	mux.HandleFunc("POST /v1/logs", ps.LogsHandler)
	mux.HandleFunc("POST /v1/metrics", ps.MetricsHandler)
	return mux
}

type otlpReceiver struct {
	t      *testctx.T
	traces sdktrace.SpanExporter
	logs   sdklog.Exporter
}

func (o *otlpReceiver) TracesHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("error reading body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		slog.Error("error unmarshalling trace request", "payload", string(body), "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	spans := telemetry.SpansFromPB(req.ResourceSpans)
	o.t.Logf("tests received %d spans", len(spans))
	if err := o.traces.ExportSpans(r.Context(), spans); err != nil {
		slog.Error("error exporting spans", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Forward to the original telemetry so we can see it there too
	if len(telemetry.SpanProcessors) > 0 {
		telemetry.SpanForwarder{
			Processors: telemetry.SpanProcessors,
		}.ExportSpans(r.Context(), spans)
	}

	w.WriteHeader(http.StatusCreated)
}

func (o *otlpReceiver) LogsHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("error reading body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		slog.Error("error unmarshalling logs request", "payload", string(body), "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	logs := telemetry.LogsFromPB(req.ResourceLogs)
	o.t.Logf("tests received %d logs", len(logs))
	if err := o.logs.Export(r.Context(), logs); err != nil {
		slog.Error("error exporting spans", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Forward to the original telemetry so we can see it there too
	if len(telemetry.LogProcessors) > 0 {
		telemetry.LogForwarder{
			Processors: telemetry.LogProcessors,
		}.Export(r.Context(), logs)
	}

	w.WriteHeader(http.StatusCreated)
}

func (o *otlpReceiver) MetricsHandler(w http.ResponseWriter, r *http.Request) {
	// TODO
}
