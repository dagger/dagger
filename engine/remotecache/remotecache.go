// Package remotecache connects an engine to the remote cache service. Run
// keeps one WebSocket open to the service, answers its requests through the
// engine's Adapter, and asks it for fresh download addresses when a read of
// an offered part needs one. The engine serves from its first second whether
// or not the connection is up, and Run never waits on the service to return.
package remotecache

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/protocol"
)

// The engine side's limits.
const (
	// ExportsInFlight is how many exports the engine works on at once.
	ExportsInFlight = 4
	// UploadConcurrency is how many blobs the engine uploads at once, across
	// all its exports.
	UploadConcurrency = 8

	// Reconnection backoff: from backoffMin, doubling to backoffMax, each
	// wait moved by up to backoffJitter of itself either way.
	backoffMin    = time.Second
	backoffMax    = 30 * time.Second
	backoffJitter = 0.2

	// helloTimeout bounds the wait for welcome after dialling.
	helloTimeout = 30 * time.Second

	// A blob upload that stalls fails, and frees its slot: uploadIdleTimeout
	// bounds the time the blob store goes without reading any of the body,
	// and uploadResponseTimeout the wait for its response once the body is
	// sent.
	uploadIdleTimeout     = time.Minute
	uploadResponseTimeout = time.Minute
)

// Config configures Run.
type Config struct {
	// URL is the service's base URL; Run dials protocol.EnginePath under it.
	URL string
	// Token is the engine token, sent as the Basic username.
	Token string
	// EngineVersion and EngineName are sent in hello.
	EngineVersion string
	EngineName    string
	// Logger receives the connection's warnings and errors. Nil discards.
	Logger *slog.Logger
}

// Adapter is the engine's side of the service's requests: its cache
// operations by entry number, and the renewal mailbox.
type Adapter interface {
	// CacheIdentity returns the engine cache's identity and generation.
	CacheIdentity() (cacheID string, generation uint64)
	// Export captures the requested roots and calls consume exactly once,
	// while the captured chains stay open. The export's Bundle is nil when no
	// root survived. The chains are released when consume returns.
	Export(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error
	// Merge merges a bundle of the service's values into the cache.
	Merge(ctx context.Context, req protocol.Merge) (protocol.Merged, error)
	// OfferParts places offered parts on the cache's entries by number.
	OfferParts(ctx context.Context, req protocol.Offer) (protocol.Offered, error)
	// TakeRenewalRequest waits for the next read that needs fresh download
	// addresses; ReplyRenewal answers it.
	TakeRenewalRequest(ctx context.Context) (*dagql.RenewalRequest, error)
	ReplyRenewal(dagql.RenewalReply) dagql.RenewalReplyDisposition
}

// Export is one capture, as Adapter.Export hands it to its consumer.
type Export struct {
	// Bundle is nil when no root survived.
	Bundle  *dagql.ValueBundle
	Skipped []protocol.SkippedRoot
	// Blobs reads the bundle's layer blobs for upload.
	Blobs BlobSource
}

// BlobSource reads a layer blob of an export's chains.
type BlobSource interface {
	ReadBlob(ctx context.Context, dgst digest.Digest) (io.ReadCloser, int64, error)
}

// settings are Run's configuration with the limits and seams tests change.
type settings struct {
	Config
	dial       func(context.Context) (wsConn, error)
	put        func(ctx context.Context, url string, body io.Reader, size int64) error
	maxMessage int
	jitter     func() float64
}

func newSettings(cfg Config) *settings {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	s := &settings{
		Config:     cfg,
		put:        httpPut(uploadClient(uploadResponseTimeout), uploadIdleTimeout),
		maxMessage: protocol.MaxMessageBytes,
		jitter:     rand.Float64,
	}
	s.dial = s.dialService
	return s
}

// Run keeps the engine connected to the service until ctx is cancelled, and
// then returns nil promptly: it closes the connection without waiting for the
// service, cancels uploads, and releases the chains held for exports.
func Run(ctx context.Context, adapter Adapter, cfg Config) error {
	return newSettings(cfg).run(ctx, adapter)
}

func (s *settings) run(ctx context.Context, adapter Adapter) error {
	var b backoff
	for {
		welcomed, err := s.serve(ctx, adapter)
		select {
		case <-ctx.Done():
			// Cancellation ends Run cleanly, whatever ended the connection.
			return nil
		default:
		}
		if welcomed {
			b.reset()
		}
		wait := b.next(s.jitter())
		s.Logger.Warn("remote cache connection ended; reconnecting", "error", err, "wait", wait)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}

// serve dials, says hello and serves one connection until it ends. It reports
// whether the service welcomed the engine.
func (s *settings) serve(ctx context.Context, adapter Adapter) (bool, error) {
	conn, err := s.dial(ctx)
	if err != nil {
		return false, fmt.Errorf("dial: %w", err)
	}
	sess := newSession(ctx, conn, adapter, s)
	defer sess.close()
	return sess.serve()
}

func (s *settings) dialService(ctx context.Context) (wsConn, error) {
	header := http.Header{}
	header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(s.Token+":")))
	conn, resp, err := websocket.Dial(ctx, strings.TrimSuffix(s.URL, "/")+protocol.EnginePath, &websocket.DialOptions{
		HTTPHeader:      header,
		CompressionMode: websocket.CompressionNoContextTakeover,
	})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(protocol.MaxMessageBytes)
	return websocketConn{conn}, nil
}

// wsConn is the connection a session uses: coder/websocket's Conn, which may
// be written, pinged and closed from any goroutine, and read from one.
type wsConn interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, data []byte) error
	Ping(ctx context.Context) error
	CloseNow() error
}

type websocketConn struct{ c *websocket.Conn }

func (w websocketConn) Read(ctx context.Context) ([]byte, error) {
	for {
		typ, data, err := w.c.Read(ctx)
		if err != nil {
			return nil, err
		}
		if typ == websocket.MessageText {
			return data, nil
		}
	}
}

func (w websocketConn) Write(ctx context.Context, data []byte) error {
	return w.c.Write(ctx, websocket.MessageText, data)
}

func (w websocketConn) Ping(ctx context.Context) error { return w.c.Ping(ctx) }
func (w websocketConn) CloseNow() error                { return w.c.CloseNow() }

// backoff is the reconnection wait: backoffMin doubling to backoffMax, each
// wait moved by up to backoffJitter of itself either way.
type backoff struct{ attempt int }

func (b *backoff) reset() { b.attempt = 0 }

// next returns the next wait; jitter is uniform in [0, 1).
func (b *backoff) next(jitter float64) time.Duration {
	wait := backoffMin << min(b.attempt, 5)
	if wait > backoffMax {
		wait = backoffMax
	}
	b.attempt++
	return time.Duration(float64(wait) * (1 + backoffJitter*(2*jitter-1)))
}

// uploadClient is the HTTP client for blob uploads: the default transport,
// with a bound on the wait for the response once the request is sent.
func uploadClient(responseTimeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = responseTimeout
	return &http.Client{Transport: transport}
}

// errUploadStalled is the error of an upload whose body the blob store
// stopped reading.
var errUploadStalled = errors.New("the blob store stopped reading the body")

// httpPut uploads a blob to a presigned URL. Its errors never include the URL,
// which carries a signature. Until the request is written, the upload is
// cancelled once no bytes of the body have been read for idle.
func httpPut(client *http.Client, idle time.Duration) func(context.Context, string, io.Reader, int64) error {
	return func(ctx context.Context, address string, body io.Reader, size int64) error {
		ctx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		stalled := time.AfterFunc(idle, func() { cancel(errUploadStalled) })
		defer stalled.Stop()
		// The body's end isn't the request's: over HTTP/2 the transport reads
		// the end before it sends the last bytes, which wait for the blob
		// store's flow control.
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
			WroteRequest: func(httptrace.WroteRequestInfo) { stalled.Stop() },
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, address, &idleBody{Reader: body, timer: stalled, idle: idle})
		if err != nil {
			return errors.New("invalid upload address")
		}
		req.ContentLength = size
		resp, err := client.Do(req)
		if err != nil {
			if cause := context.Cause(ctx); errors.Is(cause, errUploadStalled) {
				return fmt.Errorf("upload: %w", cause)
			}
			var urlErr *url.Error
			if errors.As(err, &urlErr) {
				err = urlErr.Err
			}
			return fmt.Errorf("upload: %w", err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("upload: %s", resp.Status)
		}
		return nil
	}
}

// idleBody is an upload's body. A read that returns bytes restarts timer.
// Once the request is written, httpPut stops the timer: from then on, the
// transport's response timeout bounds the upload.
type idleBody struct {
	io.Reader
	timer *time.Timer
	idle  time.Duration
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if n > 0 {
		b.timer.Reset(b.idle)
	}
	return n, err
}
