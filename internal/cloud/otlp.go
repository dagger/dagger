package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vito/go-sse/sse"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud/auth"
)

// Fetching a published trace back OUT of Dagger Cloud as OTLP
// (hack/designs/resume-from-trace.md §5.1) — the transport half of
// `dagger agent --trace <id>`.
//
// Three endpoints, each an SSE stream whose events are protojson-encoded OTLP
// export requests:
//
//	GET {DAGGER_CLOUD_URL}/v1/traces/{trace-id}   ExportTraceServiceRequest
//	GET {DAGGER_CLOUD_URL}/v1/logs/{trace-id}     ExportLogsServiceRequest
//	GET {DAGGER_CLOUD_URL}/v1/metrics/{trace-id}  ExportMetricsServiceRequest
//
// The fetch is UNFILTERED and whole-trace on purpose: §1's promise is the old
// session's whole TUI beside a live prompt, not a private reconstruction of
// its conversation, so everything the trace carries is streamed and handed to
// the sink.
//
// It lives here rather than in the CLI because `dagger trace` is a plausible
// second consumer later, and it does NOT replace the GraphQL-SSE client in
// trace.go: that one answers "render a huge trace cheaply" with incremental,
// lazy loading, and this one answers "rebuild a complete DAG and show all of
// it". Different requirements, both wanted.
//
// What this deliberately does not do is convert anything. §5.1 originally said
// to re-export through telemetry.SpansFromPB / ReexportLogsFromPB /
// ReexportMetricsFromPB, which is what the reference implementation
// (cmd/dagger/trace.go at 1492469b) does; slice 4 put those calls behind
// engine/telemetry.TraceImporter, which wraps them with §5.1.1's passthrough
// stamp and §5.1.2's sealing — both applied to the PROTOBUF, before
// conversion. Converting here would look like it worked and would silently
// skip both fixes, so the decoded request goes to the sink untouched.

// TraceImportSink receives the OTLP export requests a fetch decodes, and is
// told once when the stream has ended.
//
// engine/telemetry.TraceImporter is the implementation resume uses; the
// interface is here so this package stays transport-only (it has no opinion
// about sealing, passthrough stamps or where the spans finally land) and so a
// test can observe the call sequence.
//
// Seal is what turns "no end time" into a fact: a live span is exported at
// START and again at end, so a span the capture shows running is only really
// unfinished once the stream is over. FetchTrace calls it exactly once, after
// the span stream is drained.
type TraceImportSink interface {
	ImportSpans(context.Context, *coltracepb.ExportTraceServiceRequest) error
	ImportLogs(context.Context, *collogspb.ExportLogsServiceRequest) error
	ImportMetrics(context.Context, *colmetricspb.ExportMetricsServiceRequest) error
	Seal(context.Context) error
}

// otlp stream kinds; also the URL segment and the stats bucket name.
const (
	otlpTraces  = "traces"
	otlpLogs    = "logs"
	otlpMetrics = "metrics"
)

// OTLPClient streams a whole published trace out of Dagger Cloud as OTLP.
type OTLPClient struct {
	h          *http.Client
	u          *url.URL
	authHeader string
	stats      *clientStats
	// stall is how long a stream may deliver NO bytes before the fetch gives
	// up on it. Zero disables the watchdog (no test depends on that; it is
	// the natural meaning of the zero value).
	stall time.Duration
}

// defaultStallTimeout bounds how long a fetch waits on a silent stream.
//
// It exists because the observable failure mode without it is the worst one
// the CLI has: `dagger agent --trace` runs the fetch before the interactive
// loop starts, so a connection Cloud's edge drops without a FIN or RST —
// measured on a real agent trace, whose logs stream also reproducibly dies
// with an h2 INTERNAL_ERROR — leaves the command wedged on "restoring trace"
// forever, spinner live, prompt never arriving. A stored trace is a bounded
// download that should always be actively transferring, so a full minute of
// total silence means the stream is dead, not slow.
//
// The watchdog is byte-level, not event-level, on purpose: an SSE server
// may space real events arbitrarily far apart while keeping the connection
// audibly alive with comment keepalives, which never surface as events but
// do count as bytes.
const defaultStallTimeout = 60 * time.Second

// NewOTLPClient returns a client for the OTLP-over-SSE endpoints, reading the
// base URL from DAGGER_CLOUD_URL exactly as NewClient does.
//
// cloudAuth comes from auth.GetCloudAuth, the same value NewClient takes —
// passing it in rather than fetching it keeps the one interactive/credential
// -reading step at the caller, where `dagger trace` already does it.
func NewOTLPClient(ctx context.Context, cloudAuth *auth.Cloud) (*OTLPClient, error) {
	authHeader, err := otlpAuthHeader(ctx, cloudAuth)
	if err != nil {
		return nil, err
	}

	api := "https://api.dagger.cloud"
	if cloudURL := os.Getenv("DAGGER_CLOUD_URL"); cloudURL != "" {
		api = cloudURL
	}
	u, err := url.Parse(api)
	if err != nil {
		return nil, fmt.Errorf("parse cloud URL %q: %w", api, err)
	}

	return &OTLPClient{
		h:          http.DefaultClient,
		u:          u,
		authHeader: authHeader,
		stats:      newClientStats(),
		stall:      defaultStallTimeout,
	}, nil
}

// WithBaseURL points the client at a different base URL than
// DAGGER_CLOUD_URL resolved to.
//
// It exists for callers that serve the §5.1 endpoints themselves — the
// end-to-end restore test stands up its own, over a capture of a real agent
// session. The alternative, setting DAGGER_CLOUD_URL, is a process-wide
// mutation no parallel test suite can make safely, and it would reach every
// other Cloud client in the process.
func (c *OTLPClient) WithBaseURL(base string) (*OTLPClient, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("parse cloud URL %q: %w", base, err)
	}
	clone := *c
	clone.u = u
	return &clone, nil
}

// otlpAuthHeader renders the Authorization header for a Cloud credential.
//
// A Basic token is the DAGGER_CLOUD_TOKEN case and goes through
// auth.GetDaggerCloudAuth, which base64s it the way Cloud expects. OIDC is
// spelled out rather than left to the default branch: oauth2.Token.Type()
// echoes an unrecognized type back verbatim, so the default would send
// `Authorization: OIDC <token>` — NewClient translates the same credential to
// a Bearer token, and the two must not disagree about one auth mode.
func otlpAuthHeader(ctx context.Context, cloudAuth *auth.Cloud) (string, error) {
	if cloudAuth == nil || cloudAuth.Token == nil {
		return "", errors.New("not authenticated; run 'dagger login' or set DAGGER_CLOUD_TOKEN")
	}
	switch cloudAuth.Token.TokenType {
	case "Basic":
		return auth.GetDaggerCloudAuth(ctx, cloudAuth.Token.AccessToken)
	case "OIDC":
		return "Bearer " + cloudAuth.Token.AccessToken, nil
	default:
		return cloudAuth.Token.Type() + " " + cloudAuth.Token.AccessToken, nil
	}
}

// StatsSummary returns a human-readable breakdown of what the fetch pulled
// from Cloud, for --debug diagnostics.
func (c *OTLPClient) StatsSummary() string {
	return c.stats.Summary()
}

// FetchTrace streams the whole of traceID into sink and seals it.
//
// THE THREE STREAMS RUN SEQUENTIALLY, spans first. The reference
// implementation fans them out across an errgroup, and TraceImporter is
// concurrency-safe, so the temptation is real — but the SINK is what decides,
// and a sink is not required to be concurrency-safe. The OTel SDK's exporter
// interfaces say so in as many words: sdktrace.SpanExporter.ExportSpans is
// "called synchronously, so there is no concurrency safety requirement", and
// sdklog.Exporter.Export "should never be called concurrently with other
// Export calls". Slice 4 deliberately made the sinks an argument so a bare
// dagui.DB — a plain struct with no locking — can be one, and the frontend's
// own exporters only happen to serialize because they dispatch onto the UI
// goroutine. Fanning out would impose a requirement the type never stated, on
// every sink anyone ever passes. Measured, not argued: the fan-out shape trips
// the race detector on the canned capture, deep in dagui.DB.
//
// Spans FIRST, and drained, buys two more things. The seal is then trivially
// "after the span stream ended", with no window in which a late span lands
// already-sealed and keeps a stale end time. And every span a log record
// refers to is real by the time the record arrives, so the record folds onto
// it instead of minting the placeholder dagui.DB allocates for an unknown span
// ID — a parentless, never-ended span the importer cannot know about and
// therefore never seals.
//
// The cost is one startup fetch paying the sum of three round trips rather
// than the max; §5.3 already runs it under a span so the wait is visible.
func (c *OTLPClient) FetchTrace(ctx context.Context, traceID string, sink TraceImportSink) error {
	if traceID == "" {
		return errors.New("no trace ID to fetch")
	}

	if err := c.streamTraces(ctx, traceID, sink); err != nil {
		return err
	}

	// The span stream is drained: what is still running now really was left
	// running (§5.1.2). Sealing HERE rather than after all three streams keeps
	// the span half self-consistent even if a later stream fails, and nothing
	// in the log or metric streams can move the seal's bound — the importer
	// computes it from span timestamps alone.
	if err := sink.Seal(ctx); err != nil {
		return fmt.Errorf("seal imported trace: %w", err)
	}

	if err := c.streamLogs(ctx, traceID, sink); err != nil {
		return err
	}
	return c.streamMetrics(ctx, traceID, sink)
}

func (c *OTLPClient) streamTraces(ctx context.Context, traceID string, sink TraceImportSink) error {
	return c.consumeSSE(ctx, otlpTraces, traceID, func(data []byte) error {
		var req coltracepb.ExportTraceServiceRequest
		if err := protojson.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal traces: %w", err)
		}
		c.stats.addRecords(otlpTraces, countSpans(&req))
		return sink.ImportSpans(ctx, &req)
	})
}

func (c *OTLPClient) streamLogs(ctx context.Context, traceID string, sink TraceImportSink) error {
	return c.consumeSSE(ctx, otlpLogs, traceID, func(data []byte) error {
		var req collogspb.ExportLogsServiceRequest
		if err := protojson.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal logs: %w", err)
		}
		c.stats.addRecords(otlpLogs, countLogRecords(&req))
		return sink.ImportLogs(ctx, &req)
	})
}

func (c *OTLPClient) streamMetrics(ctx context.Context, traceID string, sink TraceImportSink) error {
	return c.consumeSSE(ctx, otlpMetrics, traceID, func(data []byte) error {
		var req colmetricspb.ExportMetricsServiceRequest
		if err := protojson.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal metrics: %w", err)
		}
		c.stats.addRecords(otlpMetrics, countMetrics(&req))
		return sink.ImportMetrics(ctx, &req)
	})
}

// consumeSSE connects to one of the OTLP endpoints and feeds every event's
// payload to cb.
//
// The event NAME is ignored, exactly as the reference implementation ignores
// it: these endpoints are undeployed, so their event vocabulary is unverified
// (§12), and reading a name nobody has promised would be the one assumption
// that turns a whole trace into silence. Any event carrying data is a payload;
// end of stream is end of trace.
func (c *OTLPClient) consumeSSE(ctx context.Context, kind, traceID string, cb func([]byte) error) error {
	// JoinPath, not an assignment to u.Path: a DAGGER_CLOUD_URL with a path
	// prefix (a proxy, a test server on a subpath) would otherwise have its
	// prefix silently dropped.
	endpoint := c.u.JoinPath("/v1/", kind, traceID).String()

	c.stats.addRequest(kind)
	slog.Debug("connecting to cloud OTLP SSE", "url", endpoint, "kind", kind)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request for %s: %w", kind, err)
	}
	req.Header.Set("Authorization", c.authHeader)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.h.Do(req) //nolint:bodyclose // closed by the defer below; the stall watchdog may close it early
	if err != nil {
		return fmt.Errorf("connect to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("fetch %s: %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
	}

	slog.Debug("connected to cloud OTLP SSE", "kind", kind)

	// The watchdog: a stored trace should always be transferring, so a body
	// that goes silent for the whole stall window is a dead connection —
	// Cloud's edge can drop one without a FIN or RST, and a read on it then
	// blocks forever, before the interactive loop has started. Closing the
	// body is what unblocks the reader; the flag is what tells the read
	// error apart from a real one.
	body := newProgressBody(resp.Body)
	var stalled atomic.Bool
	if c.stall > 0 {
		watchdogDone := make(chan struct{})
		defer close(watchdogDone)
		go func() {
			ticker := time.NewTicker(min(c.stall/4, time.Second))
			defer ticker.Stop()
			for {
				select {
				case <-watchdogDone:
					return
				case <-ticker.C:
					if body.idle() > c.stall {
						stalled.Store(true)
						resp.Body.Close()
						return
					}
				}
			}
		}()
	}

	reader := sse.NewReadCloser(body)
	defer reader.Close()

	// events/bytes so far: context every failure below carries, because a
	// mid-stream death (h2 reset, stall) is a CLOUD incident, and "how far
	// did it get" is the first question its report needs answered.
	var events, bytes int

	for {
		event, err := reader.Next()
		if err != nil {
			// Check the watchdog before anything else: it closes the body,
			// and what that surfaces as (a closed-body error, sometimes even
			// EOF) must not be mistaken for the end of the trace.
			if stalled.Load() {
				return fmt.Errorf("fetch %s stalled: no data for %s (after %d events, %d bytes): "+
					"the server stopped sending without ending the stream", endpoint, c.stall, events, bytes)
			}
			if errors.Is(err, io.EOF) {
				slog.Debug("cloud OTLP SSE stream ended", "kind", kind)
				return nil
			}
			// A canceled fetch is NOT an end of stream. The reference client
			// treats it as one, which for a renderer is harmless and for a
			// restore is not: half a trace would be restored from silently,
			// and §5.3 fails the command on a hole rather than guessing past
			// it.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("read SSE event from %s (after %d events, %d bytes): %w", endpoint, events, bytes, err)
		}

		if len(event.Data) == 0 {
			continue
		}
		events++
		bytes += len(event.Data)
		c.stats.addEvent(kind, len(event.Data))

		// A payload this client cannot decode is a LOST FACT — an agent's
		// state record, a call payload, a whole subtree — and §12 settled
		// that a trace which cannot be rebuilt fails the restore instead of
		// degrading. So an error here aborts the stream; the reference client
		// warns and carries on, which is right for a view and wrong for a
		// restore.
		if err := cb(event.Data); err != nil {
			return fmt.Errorf("%s stream: %w", kind, err)
		}
		// The sink consumed time the socket could not: don't bill it to the
		// server's stall budget.
		body.touch()
	}
}

// progressBody wraps a response body and remembers when it last delivered
// bytes, so the stall watchdog can tell "silent" from "slow".
type progressBody struct {
	rc   io.ReadCloser
	last atomic.Int64 // UnixNano of the last progress
}

func newProgressBody(rc io.ReadCloser) *progressBody {
	b := &progressBody{rc: rc}
	b.touch()
	return b
}

func (b *progressBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.touch()
	}
	return n, err
}

func (b *progressBody) Close() error { return b.rc.Close() }

func (b *progressBody) touch() { b.last.Store(time.Now().UnixNano()) }

func (b *progressBody) idle() time.Duration {
	return time.Since(time.Unix(0, b.last.Load()))
}

func countSpans(req *coltracepb.ExportTraceServiceRequest) int {
	var n int
	for _, resourceSpans := range req.GetResourceSpans() {
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			n += len(scopeSpans.GetSpans())
		}
	}
	return n
}

func countLogRecords(req *collogspb.ExportLogsServiceRequest) int {
	var n int
	for _, resourceLogs := range req.GetResourceLogs() {
		for _, scopeLogs := range resourceLogs.GetScopeLogs() {
			n += len(scopeLogs.GetLogRecords())
		}
	}
	return n
}

func countMetrics(req *colmetricspb.ExportMetricsServiceRequest) int {
	var n int
	for _, resourceMetrics := range req.GetResourceMetrics() {
		for _, scopeMetrics := range resourceMetrics.GetScopeMetrics() {
			n += len(scopeMetrics.GetMetrics())
		}
	}
	return n
}
