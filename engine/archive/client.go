package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

const (
	archivePath             = "/v1/telemetry/archives"
	archiveGenerationHeader = "X-Dagger-Archive-Generation"
	maxErrorResponseSize    = 64 << 10
)

// HTTPDoer is the transport required by the archive client. engine/client's
// DirectConn implements this interface and carries the connected session's
// authentication and routing metadata.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client reads telemetry archives through a connected engine's HTTP transport.
type Client struct {
	http         HTTPDoer
	baseURL      *url.URL
	stallTimeout time.Duration
}

// NewClient creates an archive client for the connected engine transport.
func NewClient(httpClient HTTPDoer) *Client {
	baseURL, _ := url.Parse("http://dagger")
	return &Client{http: httpClient, baseURL: baseURL}
}

// NewClientWithURL creates an archive client with an explicit base URL. It is
// useful for HTTP proxies and tests; connected engine clients should use
// NewClient.
func NewClientWithURL(httpClient HTTPDoer, baseURL string) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse archive base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("archive base URL must include scheme and host")
	}
	return &Client{http: httpClient, baseURL: parsed}, nil
}

// WithStallTimeout returns a shallow clone whose finite bootstrap and signal
// streams fail if any individual response-body read makes no progress for the
// configured duration. Every successful read starts a fresh idle period.
func (c *Client) WithStallTimeout(timeout time.Duration) *Client {
	if c == nil {
		return nil
	}
	clone := *c
	clone.stallTimeout = timeout
	return &clone
}

// ErrorKind groups archive failures by the recovery decision a caller should
// make.
type ErrorKind string

const (
	ErrorCleanMiss ErrorKind = "clean_miss"
	ErrorState     ErrorKind = "state"
	ErrorCorrupt   ErrorKind = "corrupt"
	ErrorTransient ErrorKind = "transient"
)

var (
	ErrCleanMiss     = errors.New("engine archive clean miss")
	ErrState         = errors.New("engine archive state failure")
	ErrCorrupt       = errors.New("engine archive corruption")
	ErrTransient     = errors.New("engine archive transient failure")
	ErrStreamStalled = errors.New("engine archive stream stalled")
)

// RequestError is a typed archive transport or protocol failure.
type RequestError struct {
	Kind       ErrorKind
	Failure    FailureKind
	State      State
	StatusCode int
	Err        error
}

func (e *RequestError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("engine archive %s: %v", e.Kind, e.Err)
	}
	return "engine archive " + string(e.Kind)
}

func (e *RequestError) Unwrap() error { return e.Err }

func (e *RequestError) Is(target error) bool {
	switch target {
	case ErrCleanMiss:
		return e.Kind == ErrorCleanMiss
	case ErrState:
		return e.Kind == ErrorState
	case ErrCorrupt:
		return e.Kind == ErrorCorrupt
	case ErrTransient:
		return e.Kind == ErrorTransient
	default:
		return false
	}
}

// IsCleanMiss reports whether the connected engine definitively has no usable
// archive and a caller may try another source.
func IsCleanMiss(err error) bool { return errors.Is(err, ErrCleanMiss) }

// ListOptions controls one archive list page. The engine also excludes the
// archive currently being captured for this connected session. ExcludeTraceID
// can omit one additional archive client-side.
type ListOptions struct {
	After          string
	Limit          int
	ExcludeTraceID string
}

// List returns one page of archives.
func (c *Client) List(ctx context.Context, opts ListOptions) (Page, error) {
	query := make(url.Values)
	if opts.After != "" {
		query.Set("after", opts.After)
	}
	if opts.Limit != 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	resp, err := c.do(ctx, http.MethodGet, archivePath, query, nil, "application/json", "", 0)
	if err != nil {
		return Page{}, err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return Page{}, err
	}
	if err := expectContentType(resp, "application/json"); err != nil {
		return Page{}, corrupt(err)
	}
	var page Page
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&page); err != nil {
		return Page{}, corrupt(fmt.Errorf("decode archive list: %w", err))
	}
	if opts.ExcludeTraceID != "" {
		filtered := page.Archives[:0]
		for _, manifest := range page.Archives {
			if manifest.TraceID != opts.ExcludeTraceID {
				filtered = append(filtered, manifest)
			}
		}
		page.Archives = filtered
	}
	return page, nil
}

// ListAll follows list cursors until the engine returns the final page.
func (c *Client) ListAll(ctx context.Context, opts ListOptions) ([]Manifest, error) {
	var manifests []Manifest
	for {
		page, err := c.List(ctx, opts)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, page.Archives...)
		if page.Next == "" {
			return manifests, nil
		}
		if page.Next <= opts.After {
			return nil, corrupt(fmt.Errorf("archive list cursor did not advance from %q to %q", opts.After, page.Next))
		}
		opts.After = page.Next
	}
}

// MetadataUpdate contains the mutable archive metadata supported by the engine.
type MetadataUpdate struct {
	Title string `json:"title"`
}

// UpdateMetadata updates an active archive owned by the connected client.
func (c *Client) UpdateMetadata(ctx context.Context, traceID string, update MetadataUpdate) error {
	body, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("encode archive metadata: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, archiveResourcePath(traceID, "metadata"), nil, bytes.NewReader(body), "", "", 0)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expectStatus(resp, http.StatusNoContent)
}

// BootstrapBatch is one decoded OTLP batch from a bootstrap response. Exactly
// one field is non-nil.
type BootstrapBatch struct {
	Traces *coltracepb.ExportTraceServiceRequest
	Logs   *collogspb.ExportLogsServiceRequest
}

// BootstrapResult is the verified immutable cut described by a bootstrap.
type BootstrapResult struct {
	Header   BootstrapHeader
	Terminal BootstrapTerminal
}

type consumerError struct{ err error }

func (e *consumerError) Error() string { return e.err.Error() }
func (e *consumerError) Unwrap() error { return e.err }

// Bootstrap fetches and verifies the framed bootstrap, validates its header
// before consuming any signals, decodes each OTLP batch, and validates its
// record counts, checksum, and terminal. The validated header is passed with
// every batch so callers can construct their fixed-cut importer before the first
// import without buffering the response.
// expectedGeneration may be empty when the caller has only a trace ID; the
// generation returned by the engine is still required and checked against the
// bootstrap header.
func (c *Client) Bootstrap(ctx context.Context, traceID, expectedGeneration string, consume func(BootstrapHeader, BootstrapBatch) error) (BootstrapResult, error) {
	resp, err := c.do(ctx, http.MethodGet, archiveResourcePath(traceID, "bootstrap"), nil, nil, BootstrapContentType, expectedGeneration, 0)
	if err != nil {
		return BootstrapResult{}, err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return BootstrapResult{}, err
	}
	if err := expectContentType(resp, BootstrapContentType); err != nil {
		return BootstrapResult{}, corrupt(err)
	}
	generation, err := responseGeneration(resp, expectedGeneration)
	if err != nil {
		return BootstrapResult{}, corrupt(err)
	}

	streamBody := c.streamBody(resp.Body)
	var streamHeader BootstrapHeader
	var traceRecords, logRecords int64
	header, terminal, err := DecodeBootstrap(streamBody, func(header BootstrapHeader) error {
		if err := validateBootstrapHeader(header, traceID, generation); err != nil {
			return err
		}
		streamHeader = header
		return nil
	}, func(kind BootstrapFrameKind, payload []byte) error {
		var batch BootstrapBatch
		switch kind {
		case BootstrapFrameTraces:
			batch.Traces = &coltracepb.ExportTraceServiceRequest{}
			if err := proto.Unmarshal(payload, batch.Traces); err != nil {
				return fmt.Errorf("decode bootstrap traces: %w", err)
			}
			traceRecords += countTraceRecords(batch.Traces)
		case BootstrapFrameLogs:
			batch.Logs = &collogspb.ExportLogsServiceRequest{}
			if err := proto.Unmarshal(payload, batch.Logs); err != nil {
				return fmt.Errorf("decode bootstrap logs: %w", err)
			}
			logRecords += countLogRecords(batch.Logs)
		default:
			return fmt.Errorf("unexpected bootstrap signal frame %d", kind)
		}
		if consume != nil {
			if err := consume(streamHeader, batch); err != nil {
				return &consumerError{err: err}
			}
		}
		return nil
	})
	result := BootstrapResult{Header: header, Terminal: terminal}
	if err != nil {
		var consumeErr *consumerError
		if errors.As(err, &consumeErr) {
			return result, consumeErr.err
		}
		if errors.Is(err, ErrBootstrapIncomplete) || errors.Is(err, ErrStreamStalled) {
			return result, transient(err)
		}
		return result, corrupt(fmt.Errorf("verify archive bootstrap: %w", err))
	}
	if terminal.TraceRecords != traceRecords || terminal.LogRecords != logRecords {
		return result, corrupt(fmt.Errorf("bootstrap terminal records are traces=%d logs=%d, decoded traces=%d logs=%d", terminal.TraceRecords, terminal.LogRecords, traceRecords, logRecords))
	}
	return result, nil
}

// StreamOptions fixes one finite signal read to a generation and high-water
// cursor. Cursor is the last batch successfully acknowledged by the caller.
type StreamOptions struct {
	Generation       string
	Cursor           int64
	HighWater        int64
	ExcludeSpanIDs   []string
	ExcludeLogRowIDs []int64
}

// Traces reads a finite framed trace stream. It returns the last safe resume
// cursor, including terminal progress across excluded rows.
func (c *Client) Traces(ctx context.Context, traceID string, opts StreamOptions, consume func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error) {
	return c.stream(ctx, traceID, "traces", opts, func(cursor int64, payload []byte) error {
		batch := &coltracepb.ExportTraceServiceRequest{}
		if err := proto.Unmarshal(payload, batch); err != nil {
			return corrupt(fmt.Errorf("decode archive traces: %w", err))
		}
		if consume == nil {
			return nil
		}
		return consume(cursor, batch)
	})
}

// Logs reads a finite framed log stream. It returns the last safe resume cursor.
func (c *Client) Logs(ctx context.Context, traceID string, opts StreamOptions, consume func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
	return c.stream(ctx, traceID, "logs", opts, func(cursor int64, payload []byte) error {
		batch := &collogspb.ExportLogsServiceRequest{}
		if err := proto.Unmarshal(payload, batch); err != nil {
			return corrupt(fmt.Errorf("decode archive logs: %w", err))
		}
		if consume == nil {
			return nil
		}
		return consume(cursor, batch)
	})
}

// Metrics reads a finite framed metric stream. It returns the last safe resume
// cursor.
func (c *Client) Metrics(ctx context.Context, traceID string, opts StreamOptions, consume func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error) {
	return c.stream(ctx, traceID, "metrics", opts, func(cursor int64, payload []byte) error {
		batch := &colmetricspb.ExportMetricsServiceRequest{}
		if err := proto.Unmarshal(payload, batch); err != nil {
			return corrupt(fmt.Errorf("decode archive metrics: %w", err))
		}
		if consume == nil {
			return nil
		}
		return consume(cursor, batch)
	})
}

func (c *Client) stream(ctx context.Context, traceID, signal string, opts StreamOptions, consume func(int64, []byte) error) (int64, error) {
	cursor := opts.Cursor
	if opts.Generation == "" {
		return cursor, errors.New("archive stream generation is required")
	}
	if cursor < 0 || opts.HighWater < 0 || cursor > opts.HighWater {
		return cursor, fmt.Errorf("invalid archive stream cursors: cursor=%d high-water=%d", cursor, opts.HighWater)
	}
	query := make(url.Values)
	if signal == "traces" {
		for _, spanID := range opts.ExcludeSpanIDs {
			query.Add("exclude_span", spanID)
		}
	}
	if signal == "logs" {
		for _, rowID := range opts.ExcludeLogRowIDs {
			query.Add("exclude_log", strconv.FormatInt(rowID, 10))
		}
	}
	resp, err := c.do(ctx, http.MethodGet, archiveResourcePath(traceID, signal), query, nil, enginetel.LiveContentType, opts.Generation, cursor)
	if err != nil {
		return cursor, err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return cursor, err
	}
	if err := expectContentType(resp, enginetel.LiveContentType); err != nil {
		return cursor, corrupt(err)
	}
	if _, err := responseGeneration(resp, opts.Generation); err != nil {
		return cursor, corrupt(err)
	}

	streamBody := c.streamBody(resp.Body)
	for {
		next, payload, terminal, err := enginetel.ReadLiveFrame(streamBody)
		if err != nil {
			if errors.Is(err, enginetel.ErrInvalidLiveFrame) {
				return cursor, corrupt(err)
			}
			return cursor, transient(fmt.Errorf("read archive %s stream: %w", signal, err))
		}
		if terminal {
			if next != opts.HighWater {
				return cursor, corrupt(fmt.Errorf("archive %s terminal cursor is %d, want fixed high-water %d", signal, next, opts.HighWater))
			}
			if next < cursor {
				return cursor, corrupt(fmt.Errorf("archive %s terminal cursor regressed from %d to %d", signal, cursor, next))
			}
			trailing, readErr := io.ReadAll(io.LimitReader(streamBody, 1))
			if readErr != nil {
				return cursor, transient(fmt.Errorf("finish archive %s stream: %w", signal, readErr))
			}
			if len(trailing) != 0 {
				return cursor, corrupt(fmt.Errorf("archive %s stream has trailing data", signal))
			}
			return next, nil
		}
		if next <= cursor || next > opts.HighWater {
			return cursor, corrupt(fmt.Errorf("archive %s cursor %d is outside (%d, %d]", signal, next, cursor, opts.HighWater))
		}
		if err := consume(next, payload); err != nil {
			return cursor, err
		}
		cursor = next
	}
}

type idleReadCloser struct {
	body    io.ReadCloser
	timeout time.Duration
	stalled atomic.Bool
}

func (r *idleReadCloser) Read(p []byte) (int, error) {
	if r.timeout <= 0 {
		return r.body.Read(p)
	}
	if r.stalled.Load() {
		return 0, ErrStreamStalled
	}
	watchdogDone := make(chan struct{})
	watchdog := time.AfterFunc(r.timeout, func() {
		defer close(watchdogDone)
		r.stalled.Store(true)
		_ = r.body.Close()
	})
	// Read synchronously: returning while another goroutine still owns p would
	// let the caller reuse the buffer and race the timed-out read. The watchdog
	// only closes the body, which unblocks this call.
	n, err := r.body.Read(p)
	if !watchdog.Stop() {
		<-watchdogDone
	}
	if r.stalled.Load() {
		return n, ErrStreamStalled
	}
	return n, err
}

func (r *idleReadCloser) Close() error { return r.body.Close() }

func (c *Client) streamBody(body io.ReadCloser) io.ReadCloser {
	if c == nil || c.stallTimeout <= 0 {
		return body
	}
	return &idleReadCloser{body: body, timeout: c.stallTimeout}
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader, accept, generation string, cursor int64) (*http.Response, error) {
	if c == nil || c.http == nil || c.baseURL == nil {
		return nil, transient(errors.New("archive HTTP client is not configured"))
	}
	target := *c.baseURL
	target.Path = strings.TrimRight(target.Path, "/") + path
	target.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create archive request: %w", err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if generation != "" {
		req.Header.Set(archiveGenerationHeader, generation)
	}
	if cursor > 0 {
		req.Header.Set(enginetel.LiveCursorHeader, strconv.FormatInt(cursor, 10))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transient(fmt.Errorf("request archive API: %w", err))
	}
	return resp, nil
}

func archiveResourcePath(traceID, resource string) string {
	return archivePath + "/" + url.PathEscape(traceID) + "/" + resource
}

func expectStatus(resp *http.Response, want int) error {
	if resp.StatusCode == want {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorResponseSize))
	var failure struct {
		Failure FailureKind `json:"error"`
		State   State       `json:"state"`
		Message string      `json:"message"`
	}
	_ = json.Unmarshal(body, &failure)
	message := strings.TrimSpace(failure.Message)
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	kind := ErrorTransient
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		kind = ErrorCleanMiss
	case http.StatusConflict:
		kind = ErrorState
	case http.StatusUnprocessableEntity:
		kind = ErrorCorrupt
	}
	return &RequestError{
		Kind: kind, Failure: failure.Failure, State: failure.State,
		StatusCode: resp.StatusCode, Err: errors.New(message),
	}
}

func expectContentType(resp *http.Response, want string) error {
	got := resp.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(got)
	if err != nil || mediaType != want {
		return fmt.Errorf("archive response content type is %q, want %q", got, want)
	}
	return nil
}

func validateBootstrapHeader(header BootstrapHeader, traceID, generation string) error {
	if header.TraceID != traceID {
		return fmt.Errorf("bootstrap trace ID %q does not match requested trace %q", header.TraceID, traceID)
	}
	if header.Generation != generation {
		return fmt.Errorf("bootstrap generation %q does not match response generation %q", header.Generation, generation)
	}
	if header.HighWater.Spans < 0 || header.HighWater.Logs < 0 || header.HighWater.Metrics < 0 {
		return errors.New("bootstrap contains a negative high-water cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, header.SealAt); err != nil {
		return fmt.Errorf("invalid bootstrap seal timestamp %q: %w", header.SealAt, err)
	}
	return nil
}

func responseGeneration(resp *http.Response, expected string) (string, error) {
	generation := resp.Header.Get(archiveGenerationHeader)
	if generation == "" {
		return "", errors.New("archive response is missing its generation")
	}
	if expected != "" && generation != expected {
		return "", fmt.Errorf("archive response generation is %q, want %q", generation, expected)
	}
	return generation, nil
}

func corrupt(err error) error {
	var requestErr *RequestError
	if errors.As(err, &requestErr) {
		return err
	}
	return &RequestError{Kind: ErrorCorrupt, Err: err}
}

func transient(err error) error {
	var requestErr *RequestError
	if errors.As(err, &requestErr) {
		return err
	}
	return &RequestError{Kind: ErrorTransient, Err: err}
}

func countTraceRecords(req *coltracepb.ExportTraceServiceRequest) int64 {
	var count int64
	for _, resource := range req.GetResourceSpans() {
		for _, scope := range resource.GetScopeSpans() {
			count += int64(len(scope.GetSpans()))
		}
	}
	return count
}

func countLogRecords(req *collogspb.ExportLogsServiceRequest) int64 {
	var count int64
	for _, resource := range req.GetResourceLogs() {
		for _, scope := range resource.GetScopeLogs() {
			count += int64(len(scope.GetLogRecords()))
		}
	}
	return count
}
