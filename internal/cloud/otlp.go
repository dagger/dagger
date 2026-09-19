package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/oauth2"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/internal/cloud/otlpstream"
)

// Fetching a published trace back OUT of Dagger Cloud as OTLP
// (hack/designs/resume-from-trace.md §5.1) — the transport half of
// `dagger agent --trace <id>`.
//
// Three endpoints, each a binary OTLP stream (the otlpstream framing —
// dagger.io#5226, which replaced the SSE-of-protojson protocol §5.1 and the
// reference implementation were written against):
//
//	GET {DAGGER_CLOUD_URL}/v1/traces/{trace-id}   ExportTraceServiceRequest
//	GET {DAGGER_CLOUD_URL}/v1/logs/{trace-id}     ExportLogsServiceRequest
//	GET {DAGGER_CLOUD_URL}/v1/metrics/{trace-id}  ExportMetricsServiceRequest
//
// Each stream is a sequence of frames: data frames carrying binary-protobuf
// export requests (empty ones are heartbeats), then a terminal frame — a real
// end-of-trace marker, which the SSE protocol never had. The framing matters
// beyond ceremony: a log record's BODY may itself contain SSE-shaped text
// (an agent trace captures its LLM provider's `data: {...}` stream verbatim),
// so a delimiter-based protocol read with an SSE parser finds "events" inside
// the payloads it failed to frame. That is not hypothetical; it is how the
// protocol mismatch actually surfaced ("unmarshal logs: unknown field
// \"type\"" — an Anthropic frame inside a log body, misread as Cloud's).
// Hence the Content-Type check below: a server that does not speak the framed
// protocol is refused up front, not misparsed.
//
// The fetch is UNFILTERED and whole-trace on purpose: §1's promise is the old
// session's whole TUI beside a live prompt, not a private reconstruction of
// its conversation, so everything the trace carries is streamed and handed to
// the sink.
//
// The same endpoints also take a SELECTION (api/otlp/stream_options.go on the
// server): the traces stream can be narrowed to the priority spans and to
// listened subtrees, the logs stream to one span's own or rolled-up records.
// FetchSpans and FetchLogs expose that for `dagger trace`, which renders a
// huge trace cheaply by loading lazily, the way the GraphQL-SSE client in
// trace.go did before these endpoints could select -- with the difference
// that these are addressed by trace ID and token alone, no org. The GraphQL
// client stays for `dagger cloud logs`.
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
// unfinished once importing has stopped. FetchTrace calls it exactly once after
// all three stream goroutines return, including on partial or failed fetches.
// Implementations need not be concurrency-safe; FetchTrace serializes all
// callbacks while fetching the streams in parallel.
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
	h     *http.Client
	u     *url.URL
	auth  *otlpAuthState
	stats *clientStats
	// stall is how long a stream may deliver NO bytes before the fetch gives
	// up on it. Zero disables the watchdog (no test depends on that; it is
	// the natural meaning of the zero value).
	stall time.Duration
}

type otlpAuthState struct {
	mu      sync.Mutex
	header  string
	token   *oauth2.Token
	refresh func(context.Context, *oauth2.Token) (*oauth2.Token, error)
}

// defaultStallTimeout bounds how long a fetch waits on a silent stream.
//
// It exists because the observable failure mode without it is the worst one
// the CLI has: `dagger agent --trace` runs the fetch before the interactive
// loop starts, so a connection Cloud's edge drops without a FIN or RST —
// measured on a real agent trace, whose logs stream reproducibly died with an
// h2 INTERNAL_ERROR — leaves the command wedged on "restoring trace" forever,
// spinner live, prompt never arriving. A stored trace is a bounded download
// that should always be actively transferring, so a full minute of total
// silence means the stream is dead, not slow.
//
// The framed protocol's terminal frame catches the CLOSED-early cases (a
// dropped connection surfaces as truncation), but a connection that stays
// open and silent still needs a clock. The watchdog is byte-level, not
// frame-level, on purpose: the server may space real payloads arbitrarily
// far apart while keeping the connection audibly alive with heartbeat
// frames, which never reach the sink but do count as bytes.
const defaultStallTimeout = 60 * time.Second

// ErrStreamStalled identifies a stream that exceeded its idle timeout.
var ErrStreamStalled = errors.New("cloud OTLP stream stalled")

// NewOTLPClient returns a client for the binary OTLP stream endpoints,
// reading the base URL from DAGGER_CLOUD_URL exactly as NewClient does.
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

	token := *cloudAuth.Token
	authState := &otlpAuthState{
		header: authHeader,
		token:  &token,
	}
	if token.RefreshToken != "" && token.TokenType != "Basic" && token.TokenType != "OIDC" {
		authState.refresh = auth.RefreshToken
	}

	return &OTLPClient{
		h:     http.DefaultClient,
		u:     u,
		auth:  authState,
		stats: newClientStats(),
		stall: defaultStallTimeout,
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

// WithStallTimeout returns a copy that stops any stream after it delivers
// no bytes for timeout. The timeout measures idle time, not total fetch time;
// Heartbeat frames and payloads both reset it. A non-positive timeout disables the
// watchdog.
func (c *OTLPClient) WithStallTimeout(timeout time.Duration) *OTLPClient {
	clone := *c
	clone.stall = timeout
	return &clone
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

func (a *otlpAuthState) currentHeader() string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.header
}

func (a *otlpAuthState) canRefresh() bool {
	return a != nil && a.refresh != nil
}

// refreshHeader holds the lock across the grant so concurrent 401 responses
// cannot spend the same refresh token. A waiter compares the header its failed
// request used and reuses the winner's result instead of refreshing again.
func (a *otlpAuthState) refreshHeader(ctx context.Context, usedHeader string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.header != usedHeader {
		return a.header, nil
	}

	refreshed, err := a.refresh(ctx, a.token)
	if err != nil {
		return "", err
	}
	header, err := otlpAuthHeader(ctx, &auth.Cloud{Token: refreshed})
	if err != nil {
		return "", fmt.Errorf("render refreshed authorization: %w", err)
	}
	a.token = refreshed
	a.header = header
	return header, nil
}

// StatsSummary returns a human-readable breakdown of what the fetch pulled
// from Cloud, for --debug diagnostics.
func (c *OTLPClient) StatsSummary() string {
	return c.stats.Summary()
}

// FetchTrace streams the whole of traceID into sink and seals it.
//
// The three network streams run concurrently so a slow or blocked endpoint
// does not prevent the others from transferring. Sink callbacks remain
// serialized: TraceImportSink deliberately makes no concurrency-safety
// promise, and common sinks (including the OTel SDK exporters and a bare
// dagui.DB) require callers not to invoke exports concurrently.
//
// Once all three streams stop, no late span callbacks remain. FetchTrace then
// calls Seal exactly once with the parent context, even after a stream error,
// so partial span snapshots become a consistent bounded import. A seal error
// is joined with the stream error rather than masking it.
func (c *OTLPClient) FetchTrace(ctx context.Context, traceID string, sink TraceImportSink) error {
	if traceID == "" {
		return errors.New("no trace ID to fetch")
	}

	sink = &serializedTraceImportSink{sink: sink}
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		return c.streamTraces(groupCtx, traceID, sink)
	})
	group.Go(func() error {
		return c.streamLogs(groupCtx, traceID, sink)
	})
	group.Go(func() error {
		return c.streamMetrics(groupCtx, traceID, sink)
	})
	streamErr := group.Wait()

	var sealErr error
	if err := sink.Seal(ctx); err != nil {
		sealErr = fmt.Errorf("seal imported trace: %w", err)
	}
	return errors.Join(streamErr, sealErr)
}

// SpanSelection narrows a /v1/traces stream. It mirrors the server's
// parseTraceStreamOptions (api/otlp/stream_options.go in dagger.io), which
// shares its query layer with the spansUpdated GraphQL subscription, so
// these have the semantics the Cloud web UI drives:
//
//   - Root selects the root span(s) and their visible children; the roots
//     also anchor completion, so a stream with Root set ends once every root
//     has been over for a few seconds. The zero value is the server's
//     default (true), spelled out on the wire either way.
//   - Listen adds each span, its children and their passthrough descendants,
//     and anchors completion on them too. At most 256 per request.
//   - Incremental forces the priority-only selection regardless of trace
//     size; without it a trace under the server's threshold comes back
//     whole even when Listen is set.
//   - Before/After bound spans by their Cloud-side update time. Before also
//     bounds the STREAM: one poll, then the terminal frame -- which is what
//     makes a backfill on a still-running span return instead of follow.
//   - DagUIView asks for the dagger.io/ui.* egress attributes (child count,
//     has-logs, partial, update time; engine/telemetryattrs) an incremental,
//     lazily-expanding view needs and the OTLP span form otherwise lacks.
type SpanSelection struct {
	NoRoot      bool
	Listen      []string
	Incremental bool
	Before      *time.Time
	After       *time.Time
	DagUIView   bool
}

func (s SpanSelection) query() url.Values {
	q := url.Values{}
	q.Set("root", strconv.FormatBool(!s.NoRoot))
	for _, id := range s.Listen {
		q.Add("listen", id)
	}
	if s.Incremental {
		q.Set("incremental", "true")
	}
	if s.Before != nil {
		q.Set("before", s.Before.UTC().Format(time.RFC3339Nano))
	}
	if s.After != nil {
		q.Set("after", s.After.UTC().Format(time.RFC3339Nano))
	}
	if s.DagUIView {
		q.Set("view", "dagui")
	}
	return q
}

// Log record classes a /v1/logs stream can be narrowed to (the server's
// `records` parameter). The zero value is every record.
const (
	// LogRecordsAll is every log row: text output and the semantic records
	// riding the log channel (call payloads, progress, agent state, span
	// names).
	LogRecordsAll = ""
	// LogRecordsLogs is text output only, with the global/verbose records
	// dropped -- what a rolled-up "show me this span's output" wants. It
	// requires a span ID.
	LogRecordsLogs = "logs"
	// LogRecordsCallPayloads is the dagql call payload records alone.
	LogRecordsCallPayloads = "call_payloads"
)

// LogSelection narrows a /v1/logs stream to one span's records, mirroring
// the server's parseLogStreamOptions and the logsEmitted subscription:
//
//   - SpanID selects the span; Descendants rolls its subtree up too, walking
//     past boundary/encapsulated/internal spans the way the UI does. The
//     stream ends once the selected span has ended.
//   - After bounds records by timestamp.
//   - Records picks the record class (LogRecords*).
type LogSelection struct {
	SpanID      string
	Descendants bool
	After       *time.Time
	Records     string
}

func (s LogSelection) query() url.Values {
	q := url.Values{}
	if s.SpanID != "" {
		q.Set("span_id", s.SpanID)
	}
	if s.Descendants {
		q.Set("descendants", "true")
	}
	if s.After != nil {
		q.Set("after", s.After.UTC().Format(time.RFC3339Nano))
	}
	if s.Records != LogRecordsAll {
		q.Set("records", s.Records)
	}
	return q
}

// FetchSpans streams the spans sel selects from traceID to cb, one export
// request per data frame, until the server's terminal frame. Unlike
// FetchTrace it neither seals nor touches logs and metrics: the caller is
// assembling a view incrementally and owns that bookkeeping.
func (c *OTLPClient) FetchSpans(ctx context.Context, traceID string, sel SpanSelection, cb func(context.Context, *coltracepb.ExportTraceServiceRequest) error) error {
	if traceID == "" {
		return errors.New("no trace ID to fetch")
	}
	return c.consumeStream(ctx, otlpTraces, traceID, sel.query(), func(data []byte) error {
		var req coltracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal traces: %w", err)
		}
		c.stats.addRecords(otlpTraces, countSpans(&req))
		return cb(ctx, &req)
	})
}

// FetchLogs streams the log records sel selects from traceID to cb, one
// export request per data frame, until the server's terminal frame.
func (c *OTLPClient) FetchLogs(ctx context.Context, traceID string, sel LogSelection, cb func(context.Context, *collogspb.ExportLogsServiceRequest) error) error {
	if traceID == "" {
		return errors.New("no trace ID to fetch")
	}
	return c.consumeStream(ctx, otlpLogs, traceID, sel.query(), func(data []byte) error {
		var req collogspb.ExportLogsServiceRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal logs: %w", err)
		}
		c.stats.addRecords(otlpLogs, countLogRecords(&req))
		return cb(ctx, &req)
	})
}

// serializedTraceImportSink preserves TraceImportSink's synchronous callback
// contract while FetchTrace overlaps the three network streams.
type serializedTraceImportSink struct {
	mu   sync.Mutex
	sink TraceImportSink
}

func (s *serializedTraceImportSink) ImportSpans(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sink.ImportSpans(ctx, req)
}

func (s *serializedTraceImportSink) ImportLogs(ctx context.Context, req *collogspb.ExportLogsServiceRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sink.ImportLogs(ctx, req)
}

func (s *serializedTraceImportSink) ImportMetrics(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sink.ImportMetrics(ctx, req)
}

func (s *serializedTraceImportSink) Seal(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sink.Seal(ctx)
}

func (c *OTLPClient) streamTraces(ctx context.Context, traceID string, sink TraceImportSink) error {
	return c.consumeStream(ctx, otlpTraces, traceID, nil, func(data []byte) error {
		var req coltracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal traces: %w", err)
		}
		c.stats.addRecords(otlpTraces, countSpans(&req))
		return sink.ImportSpans(ctx, &req)
	})
}

func (c *OTLPClient) streamLogs(ctx context.Context, traceID string, sink TraceImportSink) error {
	return c.consumeStream(ctx, otlpLogs, traceID, nil, func(data []byte) error {
		var req collogspb.ExportLogsServiceRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal logs: %w", err)
		}
		c.stats.addRecords(otlpLogs, countLogRecords(&req))
		return sink.ImportLogs(ctx, &req)
	})
}

func (c *OTLPClient) streamMetrics(ctx context.Context, traceID string, sink TraceImportSink) error {
	return c.consumeStream(ctx, otlpMetrics, traceID, nil, func(data []byte) error {
		var req colmetricspb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal metrics: %w", err)
		}
		c.stats.addRecords(otlpMetrics, countMetrics(&req))
		return sink.ImportMetrics(ctx, &req)
	})
}

func (c *OTLPClient) openStream(ctx context.Context, kind, endpoint string) (*http.Response, error) {
	usedHeader := c.auth.currentHeader()
	resp, err := c.requestStream(ctx, kind, endpoint, usedHeader)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}

	status := resp.StatusCode
	unauthorizedErr := closeResponseError(endpoint, resp)
	if status != http.StatusUnauthorized || !c.auth.canRefresh() {
		return nil, unauthorizedErr
	}

	refreshedHeader, err := c.auth.refreshHeader(ctx, usedHeader)
	if err != nil {
		return nil, errors.Join(unauthorizedErr, fmt.Errorf("refresh cloud OAuth credential: %w", err))
	}

	resp, err = c.requestStream(ctx, kind, endpoint, refreshedHeader)
	if err != nil {
		return nil, errors.Join(unauthorizedErr, fmt.Errorf("retry after refreshing authorization: %w", err))
	}
	if resp.StatusCode != http.StatusOK {
		retryErr := closeResponseError(endpoint, resp)
		return nil, errors.Join(unauthorizedErr, fmt.Errorf("retry after refreshing authorization: %w", retryErr))
	}
	return resp, nil
}

func (c *OTLPClient) requestStream(ctx context.Context, kind, endpoint, authHeader string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", kind, err)
	}
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Accept", otlpstream.ContentType)

	c.stats.addRequest(kind)
	resp, err := c.h.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", endpoint, err)
	}
	return resp, nil
}

func closeResponseError(endpoint string, resp *http.Response) error {
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	closeErr := resp.Body.Close()

	err := fmt.Errorf("fetch %s: %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
	if readErr != nil {
		err = errors.Join(err, fmt.Errorf("read error response: %w", readErr))
	}
	if closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close error response: %w", closeErr))
	}
	return err
}

// consumeStream connects to one of the OTLP endpoints, narrowed by query when
// given, and feeds every data frame's payload to cb.
//
// End of trace is the TERMINAL frame, and only the terminal frame: a
// connection that ends without one was truncated — half a trace must fail
// the restore (§12) rather than be restored from silently — and a server
// that answers in some other protocol entirely is refused by Content-Type
// before a byte of it is parsed.
func (c *OTLPClient) consumeStream(ctx context.Context, kind, traceID string, query url.Values, cb func([]byte) error) error {
	// JoinPath, not an assignment to u.Path: a DAGGER_CLOUD_URL with a path
	// prefix (a proxy, a test server on a subpath) would otherwise have its
	// prefix silently dropped.
	u := c.u.JoinPath("/v1/", kind, traceID)
	u.RawQuery = query.Encode()
	endpoint := u.String()

	slog.Debug("connecting to cloud OTLP stream", "url", endpoint, "kind", kind)

	resp, err := c.openStream(ctx, kind, endpoint) //nolint:bodyclose // Closed by the defer below; the watchdog also closes stalled bodies.
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("fetch %s: %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
	}

	// The tripwire the SSE-era client lacked: a server that does not speak
	// the framed protocol (an old deployment, a proxy's error page) must be
	// refused HERE. Scanning a mis-negotiated body for frames is how this
	// client once "found" its trace's own captured LLM stream and reported
	// an unmarshal error three layers away from the real mismatch.
	contentType := resp.Header.Get("Content-Type")
	if mediaType, _, err := mime.ParseMediaType(contentType); err != nil || mediaType != otlpstream.ContentType {
		return fmt.Errorf("fetch %s: server sent Content-Type %q, want %q: "+
			"it does not speak the binary OTLP stream protocol this client expects",
			endpoint, contentType, otlpstream.ContentType)
	}

	slog.Debug("connected to cloud OTLP stream", "kind", kind)

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

	// payloads/bytes so far: context every failure below carries, because a
	// mid-stream death (h2 reset, stall, truncation) is a CLOUD incident, and
	// "how far did it get" is the first question its report needs answered.
	var payloads, bytes int

	var lastCursor uint64
	var haveCursor bool

	for {
		frame, err := otlpstream.ReadFrame(body)
		if err != nil {
			// Check the watchdog before anything else: it closes the body,
			// and what that surfaces as (a closed-body error, sometimes even
			// EOF) must not be mistaken for anything the server said.
			if stalled.Load() {
				return fmt.Errorf("%w: fetch %s: no data for %s (after %d payloads, %d bytes): "+
					"the server stopped sending without ending the stream", ErrStreamStalled, endpoint, c.stall, payloads, bytes)
			}
			// A canceled fetch is NOT a server failure; report the caller's
			// cancellation as itself.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("fetch %s truncated (after %d payloads, %d bytes): "+
					"the connection ended before the stream's terminal frame", endpoint, payloads, bytes)
			}
			return fmt.Errorf("read OTLP stream frame from %s (after %d payloads, %d bytes): %w",
				endpoint, payloads, bytes, err)
		}
		if haveCursor && frame.Cursor <= lastCursor {
			return fmt.Errorf("fetch %s: stream cursor went backwards (%d after %d)",
				endpoint, frame.Cursor, lastCursor)
		}
		lastCursor, haveCursor = frame.Cursor, true

		switch frame.Kind {
		case otlpstream.FrameTerminal:
			if len(frame.Payload) != 0 {
				return fmt.Errorf("fetch %s: terminal frame has a %d-byte payload", endpoint, len(frame.Payload))
			}
			slog.Debug("cloud OTLP stream ended", "kind", kind, "payloads", payloads, "bytes", bytes)
			return nil
		case otlpstream.FrameError:
			if !utf8.Valid(frame.Payload) {
				return fmt.Errorf("fetch %s: the server reported an error the client could not decode", endpoint)
			}
			return fmt.Errorf("fetch %s: server error (after %d payloads, %d bytes): %s",
				endpoint, payloads, bytes, string(frame.Payload))
		case otlpstream.FrameData:
			// An empty data frame is a heartbeat: connection liveness, not a
			// payload. Its bytes already reset the stall clock via body.
			if len(frame.Payload) == 0 {
				continue
			}
			payloads++
			bytes += len(frame.Payload)
			c.stats.addEvent(kind, len(frame.Payload))

			// A payload this client cannot decode is a LOST FACT — an agent's
			// state record, a call payload, a whole subtree — and §12 settled
			// that a trace which cannot be rebuilt fails the restore instead
			// of degrading. So an error here aborts the stream; the reference
			// client warns and carries on, which is right for a view and
			// wrong for a restore.
			if err := cb(frame.Payload); err != nil {
				return fmt.Errorf("%s stream: %w", kind, err)
			}
			// The sink consumed time the socket could not: don't bill it to
			// the server's stall budget.
			body.touch()
		}
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
