package idtui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/koron-go/prefixw"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
	"gotest.tools/v3/golden"

	"dagger.io/dagger/telemetry"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/util/scrub"
	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
)

func TestMain(m *testing.M) {
	os.Exit(oteltest.Main(m))
}

func Middleware() []testctx.Middleware[*testing.T] {
	return []testctx.Middleware[*testing.T]{
		oteltest.WithTracing(
			oteltest.TraceConfig[*testing.T]{
				StartOptions: testutil.SpanOpts[*testing.T],
			},
		),
		oteltest.WithLogging[*testing.T](),
	}
}

type TelemetrySuite struct {
	Home string
}

func TestTelemetry(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(TelemetrySuite{
		Home: t.TempDir(),
	})
}

func (s TelemetrySuite) TestGolden(ctx context.Context, t *testctx.T) {
	// setup a git repo so function call tests can pick up the right metadata

	// remove the repo if it exists now too, since the Cleanup doesn't always run, e.g. after a ctrl-C
	exec.Command("rm", "-rf", ".git").Run()

	cmd := exec.Command("sh", "-c", "git init && git remote add origin git@github.com:dagger/dagger")
	if co, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to initialize viztest git repo: %v: (%s)", err, co)
	}

	t.Cleanup(func() {
		exec.Command("rm", "-rf", ".git").Run()
	})

	listDir := t.TempDir()
	os.Create(filepath.Join(listDir, "test.txt"))
	os.Create(filepath.Join(listDir, "test2.txt"))
	os.Create(filepath.Join(listDir, "test3.txt"))

	for _, ex := range []Example{
		// implementations of these functions can be found in viztest/main.go
		{Function: "hello-world"},
		{Function: "fail-log", Fail: true},
		{Function: "fail-effect", Fail: true},
		{Function: "fail-log-native", Fail: true},
		{Function: "encapsulate"},
		{Function: "fail-encapsulated", Fail: true},
		{Function: "pending", Fail: true, RevealNoisySpans: true},
		{Function: "list", Args: []string{"--dir", listDir}},
		{Function: "object-lists"},
		{Function: "nested-calls"},
		{Function: "path-args", Args: []string{"--file", "golden_test.go", "--dir", listDir}},
		{
			Function: "custom-span",
			Env: []string{
				"OTEL_RESOURCE_ATTRIBUTES=foo=bar,fizz=buzz",
			},
			DBTest: func(t *testctx.T, db *dagui.DB) {
				require.NotEmpty(t, db.Spans.Order)
				resource := db.FindResource(semconv.ServiceName("dagger-cli"))
				require.NotNil(t, resource)
				attrs := resource.Attributes()
				require.Contains(t, attrs, attribute.String("foo", "bar"))
				require.Contains(t, attrs, attribute.String("fizz", "buzz"))
			},
		},
		{Function: "use-exec-service"},
		{Function: "use-no-exec-service"},
		{Function: "docker-build", Args: []string{
			"with-exec", "--args", "echo,hey",
			"stdout",
		}},
		{Function: "docker-build-fail", Args: []string{
			"with-exec", "--args", "echo,hey",
			"stdout",
		}, Fail: true},
		{Function: "revealed-spans"},

		{Function: "git-readme", Args: []string{
			"--remote", "https://github.com/dagger/dagger",
			"--version", "v0.18.6",
		}},
		{Function: "httpreadme", Args: []string{
			"--remote", "https://github.com/dagger/dagger",
			"--version", "v0.18.6",
		}},

		// tests intended to trigger consistent tui exec metrics output
		{Function: "disk-metrics", Verbosity: 3, FuzzyTest: func(t *testctx.T, out string) {
			require.NotEmpty(t, out)

			lines := strings.Split(out, "\n")
			var ddLine string
			for _, line := range lines {
				if strings.Contains(line, "dd if=/dev/urandom") {
					ddLine = line
					break
				}
			}

			require.NotEmpty(t, ddLine, "line containing 'dd if=/dev/urandom' not found")
			require.Contains(t, ddLine, "| Disk Write: X.X B")
			require.Contains(t, ddLine, "| Memory Bytes (current): X.X B")
			require.Contains(t, ddLine, "| Memory Bytes (peak): X.X B")

			// note cpu pressure, io pressure, and network stats are not tested here. they only appear when nonzero.
		}, Flaky: "Depends on details of the engine runner (e.g. fails in Windows + WSL2)"},

		// test that directly using a broken module surfaces the error
		{Module: "./viztest/broken-dep/broken", Function: "broken", Fail: true},
		// test that a module with a broken dependency surfaces the error
		{Module: "./viztest/broken-dep", Function: "use-broken", Fail: true},
		// test that a module with an unloadable dependency surfaces the error
		{Module: "./viztest/broken-dep-sdk", Function: "use-invalid", Fail: true},

		// test that module function call errors are properly stamped with their origin
		{Function: "call-failing-dep", Fail: true},
		{Function: "call-bubbling-dep", Fail: true},
		{Function: "fail-multi", Fail: true},
		{Name: "fail-multi-noexpand", Function: "fail-multi", Fail: true, NoExpand: true},

		// FIXME: these constantly fail in CI/Dagger, but not against a local
		// engine. spent a day investigating, don't have a good explanation. it
		// fails because despite the warmup running to completion, the test gets a
		// cache miss.
		{Function: "cached-execs", Flaky: "nested Dagger causes cache misses"},
		{Function: "use-cached-exec-service", Flaky: "nested Dagger causes cache misses"},

		// Python SDK tests
		{Module: "./viztest/python", Function: "pending", Fail: true, RevealNoisySpans: true},
		{Module: "./viztest/python", Function: "custom-span"},

		// TypeScript SDK tests
		{Module: "./viztest/typescript", Function: "pending", Fail: true, RevealNoisySpans: true},
		{Module: "./viztest/typescript", Function: "custom-span"},
		{Module: "./viztest/typescript", Function: "fail-log", Fail: true},
		{Module: "./viztest/typescript", Function: "fail-effect", Fail: true},
		{Module: "./viztest/typescript", Function: "fail-log-native", Fail: true},
		// local module calls local module fn
		{
			Function: "trace-function-calls",
			DBTest: func(t *testctx.T, db *dagui.DB) {
				require.NotEmpty(t, db.Spans.Order)
				var depCalled, rootCalled bool
				for _, s := range db.Spans.Order {
					switch s.Name {
					case "Dep.getFiles":
						require.Equal(t, "Viztest.TraceFunctionCalls", strAttr(t, s, telemetry.ModuleCallerFunctionCallNameAttr))
						require.Equal(t, "github.com/dagger/dagger/viztest", strings.Split(strAttr(t, s, telemetry.ModuleCallerRefAttr), "@")[0])
						require.Equal(t, "Dep.getFiles", strAttr(t, s, telemetry.ModuleFunctionCallNameAttr))
						require.Equal(t, "github.com/dagger/dagger/viztest/dep", strings.Split(strAttr(t, s, telemetry.ModuleRefAttr), "@")[0])
						depCalled = true
					case "Viztest.traceFunctionCalls":
						require.Equal(t, "", strAttr(t, s, telemetry.ModuleCallerFunctionCallNameAttr))
						require.Equal(t, "", strAttr(t, s, telemetry.ModuleCallerRefAttr))
						require.Equal(t, "Viztest.traceFunctionCalls", strAttr(t, s, telemetry.ModuleFunctionCallNameAttr))
						require.Equal(t, "github.com/dagger/dagger/viztest",
							strings.Split(strAttr(t, s, telemetry.ModuleRefAttr), "@")[0])
						rootCalled = true
					}
				}
				require.True(t, rootCalled)
				require.True(t, depCalled)
			},
		},
		// local module calls remote module fn
		{
			Function: "trace-remote-function-calls",
			DBTest: func(t *testctx.T, db *dagui.DB) {
				require.NotEmpty(t, db.Spans.Order)
				var depCalled, rootCalled bool
				for _, s := range db.Spans.Order {
					switch s.Name {
					case "Versioned.hello":
						require.Equal(t, "Viztest.TraceRemoteFunctionCalls", strAttr(t, s, telemetry.ModuleCallerFunctionCallNameAttr))
						require.Equal(t, "github.com/dagger/dagger/viztest", strings.Split(strAttr(t, s, telemetry.ModuleCallerRefAttr), "@")[0])
						require.Equal(t, "Versioned.hello", strAttr(t, s, telemetry.ModuleFunctionCallNameAttr))
						require.Equal(t, "github.com/dagger/dagger-test-modules/versioned@73670b0338c02cdd190f56b34c6e25066c7c8875", strAttr(t, s, telemetry.ModuleRefAttr))
						depCalled = true
					case "Viztest.traceRemoteFunctionCalls":
						require.Equal(t, "", strAttr(t, s, telemetry.ModuleCallerFunctionCallNameAttr))
						require.Equal(t, "", strAttr(t, s, telemetry.ModuleCallerRefAttr))
						require.Equal(t, "Viztest.traceRemoteFunctionCalls", strAttr(t, s, telemetry.ModuleFunctionCallNameAttr))
						require.Equal(t, "github.com/dagger/dagger/viztest", strings.Split(strAttr(t, s, telemetry.ModuleRefAttr), "@")[0])
						rootCalled = true
					}
				}
				require.True(t, rootCalled)
				require.True(t, depCalled)
			},
		},
		// remote module calls local module fn
		{
			Module:   "github.com/dagger/dagger-test-modules@73670b0338c02cdd190f56b34c6e25066c7c8875",
			Function: "fn",
			DBTest: func(t *testctx.T, db *dagui.DB) {
				require.NotEmpty(t, db.Spans.Order)
				var depCalled, rootCalled bool
				for _, s := range db.Spans.Order {
					switch s.Name {
					case "DepAlias.fn":
						require.Equal(t, "RootMod.Fn", strAttr(t, s, telemetry.ModuleCallerFunctionCallNameAttr))
						require.Equal(t, "github.com/dagger/dagger-test-modules@73670b0338c02cdd190f56b34c6e25066c7c8875", strAttr(t, s, telemetry.ModuleCallerRefAttr))
						require.Equal(t, "DepAlias.fn", strAttr(t, s, telemetry.ModuleFunctionCallNameAttr))
						require.Equal(t, "github.com/dagger/dagger-test-modules/dep@73670b0338c02cdd190f56b34c6e25066c7c8875", strAttr(t, s, telemetry.ModuleRefAttr))
						depCalled = true
					case "RootMod.fn":
						require.Equal(t, "", strAttr(t, s, telemetry.ModuleCallerFunctionCallNameAttr))
						require.Equal(t, "", strAttr(t, s, telemetry.ModuleCallerRefAttr))
						require.Equal(t, "RootMod.fn", strAttr(t, s, telemetry.ModuleFunctionCallNameAttr))
						require.Equal(t, "github.com/dagger/dagger-test-modules@73670b0338c02cdd190f56b34c6e25066c7c8875", strAttr(t, s, telemetry.ModuleRefAttr))
						rootCalled = true
					}
				}
				require.True(t, rootCalled)
				require.True(t, depCalled)
			},
		},
	} {
		testName := ex.Name
		goldFile := ex.Name
		if testName == "" {
			testName = ex.Function
			goldFile = ex.Function
			if ex.Module != "" {
				// don't use `/` in testName; it confuses testctx output in dagger telemetry
				testName = strings.Join([]string{path.Base(ex.Module), ex.Function}, "-")
				goldFile = path.Join(path.Base(ex.Module), ex.Function)
			}
		}
		t.Run(testName, func(ctx context.Context, t *testctx.T) {
			goldFile := `TestTelemetry/TestGolden/` + goldFile
			out, db := ex.Run(ctx, t, s)
			switch {
			case ex.Flaky != "":
				cmp := golden.String(out, goldFile)()
				if !cmp.Success() {
					t.Log(cmp.(interface{ FailureMessage() string }).FailureMessage())
					t.Skip("Flaky: " + ex.Flaky)
				}
			case ex.FuzzyTest != nil:
				ex.FuzzyTest(t, out)
			default:
				golden.Assert(t, out, goldFile)
			}
			if ex.DBTest != nil {
				ex.DBTest(t, db)
			}
		})
	}
}

func strAttr(t testing.TB, s *dagui.Span, name string) string {
	return unmarshalAs[string](t, s.ExtraAttributes[name])
}

func unmarshalAs[T any](t testing.TB, data json.RawMessage) T {
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	return result
}

type Example struct {
	Name     string // a custom name for the example
	Module   string
	Function string
	Args     []string
	// verbosities 3 and higher do not work well with golden, they're not very deterministic atm
	Verbosity int
	Fail      bool
	// used for tests that need to see through errors (e.g. 'pending')
	RevealNoisySpans bool
	// by default, tests show the full expanded tree, unless this is set
	NoExpand bool
	// if a reason is given for Flaky, ignore failures, but log the failure and the provided explanation.
	// ineffectual if FuzzyTest is in use.
	Flaky  string
	Env    []string
	DBTest func(*testctx.T, *dagui.DB)
	// Using fuzzytest will eschew golden assertions and testdata and allow string assertions instead
	FuzzyTest func(*testctx.T, string)
}

func (ex Example) Run(ctx context.Context, t *testctx.T, s TelemetrySuite) (string, *dagui.DB) {
	db, otlpL := testDB(t)

	if ex.Module == "" {
		ex.Module = "./viztest"
	}

	daggerBin := "dagger" // $PATH
	if bin := os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN"); bin != "" {
		daggerBin = bin
	}

	daggerArgs := []string{"--progress=report", "-v", "call", "-m", ex.Module, ex.Function}
	daggerArgs = append(daggerArgs, ex.Args...)

	if ex.Verbosity > 0 {
		daggerArgs = append(daggerArgs, "-"+strings.Repeat("v", ex.Verbosity))
	}

	// For most of these tests we need to see what actually happened at least
	// within the example.
	if !ex.NoExpand {
		ex.Env = append(ex.Env, "DAGGER_EXPAND_COMPLETED=1")
	}

	if ex.RevealNoisySpans {
		ex.Env = append(ex.Env, "DAGGER_REVEAL=1")
	}

	realHome, _ := os.UserHomeDir()

	// NOTE: we care about CACHED states for these tests, so we need some way for
	// them to not be flaky (cache hit/miss), but still produce the same golden
	// output every time. So, we run everything twice. The first run will cache
	// the things that should be cacheable, and the second run will be the final
	// result. Each test is responsible for busting its own caches.
	func() {
		ctx, span := otel.Tracer("dagger.io/golden").Start(ctx, "warmup")
		defer span.End()
		warmup := exec.Command(daggerBin, daggerArgs...)
		warmup.Env = append(
			os.Environ(),
			fmt.Sprintf("HOME=%s", s.Home), // ignore any local Dagger Cloud auth
		)
		warmup.Env = append(warmup.Env, telemetry.PropagationEnv(ctx)...)
		warmup.Env = append(warmup.Env, ex.Env...)

		// still try use docker credentials even though we overrode HOME, lest we get rate limited
		if realHome != "" {
			warmup.Env = append(warmup.Env, fmt.Sprintf("DOCKER_CONFIG=%s/.docker", realHome))
		}

		warmupBuf := new(bytes.Buffer)
		defer func() {
			if t.Failed() {
				t.Logf("warmup failed! output:\n%s", warmupBuf.String())
			}
		}()
		warmup.Stderr = warmupBuf
		warmup.Stdout = warmupBuf
		err := warmup.Run()
		if ex.Fail {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}()

	cmd := exec.Command(daggerBin, daggerArgs...)
	cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("HOME=%s", s.Home), // ignore any local Dagger Cloud auth
		"NO_COLOR=1",
		"OTEL_EXPORTER_OTLP_TRACES_LIVE=1",
		fmt.Sprintf("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://%s/v1/traces", otlpL.Addr().String()),
		fmt.Sprintf("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://%s/v1/logs", otlpL.Addr().String()),
	)
	cmd.Env = append(cmd.Env, telemetry.PropagationEnv(ctx)...)
	cmd.Env = append(cmd.Env, ex.Env...)

	// still try use docker credentials even though we overrode HOME, lest we get rate limited
	if realHome != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("DOCKER_CONFIG=%s/.docker", realHome))
	}

	errBuf := new(bytes.Buffer)
	outBuf := new(bytes.Buffer)
	cmd.Stderr = io.MultiWriter(errBuf, prefixw.New(testutil.NewTWriter(t), "stderr: "))
	cmd.Stdout = io.MultiWriter(outBuf, prefixw.New(testutil.NewTWriter(t), "stdout: "))

	err := cmd.Run()
	if ex.Fail {
		require.Error(t, err)
	} else {
		require.NoError(t, err)
	}

	// NOTE: stdout/stderr are in practice interleaved based on timing, but we
	// need a stable representation, so we just keep them separate.
	var expected string
	if outBuf.Len() > 0 {
		expected += "Expected stdout:\n\n" + outBuf.String() + "\n\n"
	}
	if errBuf.Len() > 0 {
		expected += "Expected stderr:\n\n" + errBuf.String()
	}

	return scrub.Stabilize(expected), db
}

func testDB(t *testctx.T) (*dagui.DB, net.Listener) {
	db := dagui.NewDB()
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)

	srv := &http.Server{
		Handler: newReceiver(t, db, db.LogExporter()),
	}
	go srv.Serve(l)

	t.Cleanup(func() { srv.Close() })

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
	mu     sync.Mutex
}

func (o *otlpReceiver) TracesHandler(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()

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
	o.mu.Lock()
	defer o.mu.Unlock()

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

	if err := telemetry.ReexportLogsFromPB(r.Context(), o.logs, &req); err != nil {
		slog.Error("error exporting spans", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Forward to the original telemetry so we can see it there too
	if len(telemetry.LogProcessors) > 0 {
		if err := telemetry.ReexportLogsFromPB(r.Context(), telemetry.LogForwarder{
			Processors: telemetry.LogProcessors,
		}, &req); err != nil {
			slog.Warn("error forwarding logs", "error", err)
		}
	}

	w.WriteHeader(http.StatusCreated)
}

func (o *otlpReceiver) MetricsHandler(w http.ResponseWriter, r *http.Request) {
	// TODO
}
