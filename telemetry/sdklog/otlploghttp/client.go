package otlploghttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/dagger/dagger/telemetry/sdklog/otlploghttp/transform"
	"google.golang.org/protobuf/proto"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
)

const contentTypeProto = "application/x-protobuf"

// Keep it in sync with golang's DefaultTransport from net/http! We
// have our own copy to avoid handling a situation where the
// DefaultTransport is overwritten with some different implementation
// of http.RoundTripper or it's modified by other package.
var ourTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

type Config struct {
	Endpoint string
	URLPath  string
	Headers  map[string]string
	Insecure bool
	Timeout  time.Duration
}

type client struct {
	name     string
	cfg      Config
	client   *http.Client
	stopCh   chan struct{}
	stopOnce sync.Once
}

var _ sdklog.LogExporter = (*client)(nil)

func NewClient(cfg Config) sdklog.LogExporter {
	httpClient := &http.Client{
		Transport: ourTransport,
		Timeout:   cfg.Timeout,
	}

	stopCh := make(chan struct{})
	return &client{
		name:   "logs",
		cfg:    cfg,
		stopCh: stopCh,
		client: httpClient,
	}
}

// Stop shuts down the client and interrupt any in-flight request.
func (d *client) Shutdown(ctx context.Context) error {
	d.stopOnce.Do(func() {
		close(d.stopCh)
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}

// UploadLogs sends a batch of records to the collector.
func (d *client) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	pbRequest := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: transform.Logs(logs),
	}

	rawRequest, err := proto.Marshal(pbRequest)
	if err != nil {
		return err
	}

	ctx, cancel := d.contextWithStop(ctx)
	defer cancel()

	request, err := d.newRequest(rawRequest)
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	request.reset(ctx)
	resp, err := d.client.Do(request.Request)
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Temporary() {
		return newResponseError(http.Header{})
	}
	if err != nil {
		return err
	}

	if resp != nil && resp.Body != nil {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				otel.Handle(err)
			}
		}()
	}

	switch sc := resp.StatusCode; {
	case sc >= 200 && sc <= 299:
		return nil
	case sc == http.StatusTooManyRequests,
		sc == http.StatusBadGateway,
		sc == http.StatusServiceUnavailable,
		sc == http.StatusGatewayTimeout:
		// Retry-able failures.  Drain the body to reuse the connection.
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			otel.Handle(err)
		}
		return newResponseError(resp.Header)
	default:
		return fmt.Errorf("failed to send to %s: %s", request.URL, resp.Status)
	}
}

func (d *client) newRequest(body []byte) (request, error) {
	u := url.URL{Scheme: d.getScheme(), Host: d.cfg.Endpoint, Path: d.cfg.URLPath}
	r, err := http.NewRequest(http.MethodPost, u.String(), nil)
	if err != nil {
		return request{Request: r}, err
	}

	userAgent := "OTel OTLP Exporter Go/" + otlptrace.Version()
	r.Header.Set("User-Agent", userAgent)

	for k, v := range d.cfg.Headers {
		r.Header.Set(k, v)
	}
	r.Header.Set("Content-Type", contentTypeProto)

	req := request{Request: r}
	r.ContentLength = (int64)(len(body))
	req.bodyReader = bodyReader(body)

	return req, nil
}

// MarshalLog is the marshaling function used by the logging system to represent this Client.
func (d *client) MarshalLog() interface{} {
	return struct {
		Type     string
		Endpoint string
		Insecure bool
	}{
		Type:     "otlploghttp",
		Endpoint: d.cfg.Endpoint,
		Insecure: d.cfg.Insecure,
	}
}

// bodyReader returns a closure returning a new reader for buf.
func bodyReader(buf []byte) func() io.ReadCloser {
	return func() io.ReadCloser {
		return io.NopCloser(bytes.NewReader(buf))
	}
}

// request wraps an http.Request with a resettable body reader.
type request struct {
	*http.Request

	// bodyReader allows the same body to be used for multiple requests.
	bodyReader func() io.ReadCloser
}

// reset reinitializes the request Body and uses ctx for the request.
func (r *request) reset(ctx context.Context) {
	r.Body = r.bodyReader()
	r.Request = r.Request.WithContext(ctx)
}

// retryableError represents a request failure that can be retried.
type retryableError struct {
	throttle int64
}

// newResponseError returns a retryableError and will extract any explicit
// throttle delay contained in headers.
func newResponseError(header http.Header) error {
	var rErr retryableError
	if s, ok := header["Retry-After"]; ok {
		if t, err := strconv.ParseInt(s[0], 10, 64); err == nil {
			rErr.throttle = t
		}
	}
	return rErr
}

func (e retryableError) Error() string {
	return "retry-able request failure"
}

func (d *client) getScheme() string {
	if d.cfg.Insecure {
		return "http"
	}
	return "https"
}

func (d *client) contextWithStop(ctx context.Context) (context.Context, context.CancelFunc) {
	// Unify the parent context Done signal with the client's stop
	// channel.
	ctx, cancel := context.WithCancel(ctx)
	go func(ctx context.Context, cancel context.CancelFunc) {
		select {
		case <-ctx.Done():
			// Nothing to do, either cancelled or deadline
			// happened.
		case <-d.stopCh:
			cancel()
		}
	}(ctx, cancel)
	return ctx, cancel
}
