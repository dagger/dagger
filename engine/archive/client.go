package archive

import (
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
	"time"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

const (
	archivePath          = "/v1/telemetry/archives"
	maxErrorResponseSize = 64 << 10
)

// AgentBootstrapResource names the agent-restore bootstrap within an archive.
// It is agent-specific: final agent/subscription control records plus the
// recipe closure of their snapshots. The OTLP signal streams alongside it are
// generic telemetry resources.
const AgentBootstrapResource = "agent-bootstrap"

// HTTPDoer is the transport required by the archive client. engine/client's
// DirectConn implements this interface and carries the connected session's
// authentication and routing metadata.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client reads telemetry archives through a connected engine's HTTP transport.
type Client struct {
	http    HTTPDoer
	baseURL *url.URL
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
	ErrCleanMiss = errors.New("engine archive clean miss")
	ErrState     = errors.New("engine archive state failure")
	ErrCorrupt   = errors.New("engine archive corruption")
	ErrTransient = errors.New("engine archive transient failure")
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

// UnsealedArchive is an archive whose session ended without a seal. It has no
// bootstrap; stream it with StreamOptions.Unsealed at Cut.
type UnsealedArchive struct {
	Cut HighWater
}

// Unsealed looks up an interrupted or incomplete archive for a best-effort
// read of what it recorded; Cut is the current end of its store. It fails
// with an ErrState request error for any other state, including a sealed
// (closed) archive.
func (c *Client) Unsealed(ctx context.Context, traceID string) (UnsealedArchive, error) {
	resp, err := c.do(ctx, http.MethodGet, archivePath+"/"+url.PathEscape(traceID), url.Values{"unsealed": {"1"}}, "application/json", 0)
	if err != nil {
		return UnsealedArchive{}, err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return UnsealedArchive{}, err
	}
	if err := expectContentType(resp, "application/json"); err != nil {
		return UnsealedArchive{}, corrupt(err)
	}
	var manifest Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorResponseSize)).Decode(&manifest); err != nil {
		return UnsealedArchive{}, corrupt(fmt.Errorf("decode archive manifest: %w", err))
	}
	return UnsealedArchive{Cut: manifest.HighWater}, nil
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
	resp, err := c.do(ctx, http.MethodGet, archivePath, query, "application/json", 0)
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

// BootstrapBatch is one decoded OTLP logs batch from a bootstrap response.
type BootstrapBatch struct {
	Logs *collogspb.ExportLogsServiceRequest
}

// BootstrapResult is the immutable cut described by a bootstrap.
type BootstrapResult struct {
	Header   BootstrapHeader
	Terminal BootstrapTerminal
}

// Bootstrap buffers the complete raw bootstrap and checks its header and
// terminal checksum before invoking consume, so a truncated or corrupt
// bootstrap never applies part of itself. Consumers may then apply the batches
// and wait for their frontend barrier, without loading unrelated historical
// telemetry. The engine verified the roster and recipe closure when it built
// the bootstrap.
func (c *Client) Bootstrap(ctx context.Context, traceID string, consume func(BootstrapHeader, BootstrapBatch) error) (BootstrapResult, error) {
	resp, err := c.do(ctx, http.MethodGet, archiveResourcePath(traceID, AgentBootstrapResource), nil, BootstrapContentType, 0)
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

	var batches []BootstrapBatch
	header, terminal, err := DecodeBootstrap(resp.Body, func(header BootstrapHeader) error {
		return validateBootstrapHeader(header, traceID)
	}, func(payload []byte) error {
		logs := &collogspb.ExportLogsServiceRequest{}
		if err := proto.Unmarshal(payload, logs); err != nil {
			return fmt.Errorf("decode bootstrap logs: %w", err)
		}
		batches = append(batches, BootstrapBatch{Logs: logs})
		return nil
	})
	result := BootstrapResult{Header: header, Terminal: terminal}
	if err != nil {
		if errors.Is(err, ErrBootstrapIncomplete) {
			return result, transient(err)
		}
		return result, corrupt(fmt.Errorf("decode archive bootstrap: %w", err))
	}
	for _, batch := range batches {
		if consume != nil {
			if err := consume(header, batch); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

// StreamOptions fixes one finite signal read to a high-water cursor. Cursor is
// the last batch successfully acknowledged by the caller. Unsealed reads an
// interrupted or incomplete archive at the cut returned by Unsealed; its
// log stream then includes agent control records.
type StreamOptions struct {
	Cursor    int64
	HighWater int64
	Unsealed  bool
}

// Traces reads a finite framed trace stream. It returns the last safe resume
// cursor.
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
	if cursor < 0 || opts.HighWater < 0 || cursor > opts.HighWater {
		return cursor, fmt.Errorf("invalid archive stream cursors: cursor=%d high-water=%d", cursor, opts.HighWater)
	}
	query := make(url.Values)
	if opts.Unsealed {
		query.Set("unsealed", "1")
	}
	resp, err := c.do(ctx, http.MethodGet, archiveResourcePath(traceID, signal), query, enginetel.LiveContentType, cursor)
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

	for {
		kind, next, payload, err := enginetel.ReadLiveFrame(resp.Body)
		if err != nil {
			if errors.Is(err, enginetel.ErrInvalidLiveFrame) || errors.Is(err, enginetel.ErrLiveStream) {
				return cursor, corrupt(err)
			}
			return cursor, transient(fmt.Errorf("read archive %s stream: %w", signal, err))
		}
		if kind == enginetel.LiveFrameHello {
			if next != cursor {
				return cursor, corrupt(errors.New("archive hello cursor mismatch"))
			}
			continue
		}
		if kind == enginetel.LiveFrameTerminal {
			if next != opts.HighWater {
				return cursor, corrupt(fmt.Errorf("archive %s terminal cursor is %d, want fixed high-water %d", signal, next, opts.HighWater))
			}
			if next < cursor {
				return cursor, corrupt(fmt.Errorf("archive %s terminal cursor regressed from %d to %d", signal, cursor, next))
			}
			trailing, readErr := io.ReadAll(io.LimitReader(resp.Body, 1))
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

func (c *Client) do(ctx context.Context, method, path string, query url.Values, accept string, cursor int64) (*http.Response, error) {
	if c == nil || c.http == nil || c.baseURL == nil {
		return nil, transient(errors.New("archive HTTP client is not configured"))
	}
	target := *c.baseURL
	target.Path = strings.TrimRight(target.Path, "/") + path
	target.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create archive request: %w", err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
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
	case http.StatusNotFound:
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorResponseSize))
		return fmt.Errorf("archive response content type is %q, want %q: %s", got, want, strings.TrimSpace(string(body)))
	}
	return nil
}

func validateBootstrapHeader(header BootstrapHeader, traceID string) error {
	if header.TraceID != traceID {
		return fmt.Errorf("bootstrap trace ID %q does not match requested trace %q", header.TraceID, traceID)
	}
	if header.HighWater.Spans < 0 || header.HighWater.Logs < 0 || header.HighWater.Metrics < 0 {
		return errors.New("bootstrap contains a negative high-water cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, header.SealAt); err != nil {
		return fmt.Errorf("invalid bootstrap seal timestamp %q: %w", header.SealAt, err)
	}
	return nil
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
