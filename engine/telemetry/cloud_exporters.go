package telemetry

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/dagger/dagger/internal/cloud/auth"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"golang.org/x/oauth2"
)

// NewCloudExporters builds OTLP exporters for Dagger Cloud from a Cloud
// credential. cloudURL is the Cloud API URL; when empty it falls back to
// DAGGER_CLOUD_URL and then the default Cloud API URL.
//
// Basic (engine token) and OIDC credentials are sent as a static
// Authorization header. OAuth access tokens expire, so the exporters instead
// share an HTTP client that sets the current bearer token on each request,
// refreshed through tokenRefreshFn when it is non-nil.
//
// Each exporter stamps the X-Dagger-Export sequence of its own writer, and
// uploads use the private Cloud export transport. Every request is bounded by
// CloudExportTimeout: the OTLP exporters apply their own default timeout only
// to a client they build themselves.
func NewCloudExporters(ctx context.Context, cloudAuth *auth.Cloud, tokenRefreshFn func(context.Context) (*oauth2.Token, error), cloudURL string) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, error) {
	return newCloudExporters(ctx, cloudAuth, tokenRefreshFn, cloudURL, CloudExportTimeout)
}

// CloudExportTimeout bounds one request of a Cloud exporter, the OTLP HTTP
// exporters' own default.
const CloudExportTimeout = 10 * time.Second

func newCloudExporters(ctx context.Context, cloudAuth *auth.Cloud, tokenRefreshFn func(context.Context) (*oauth2.Token, error), cloudURL string, requestTimeout time.Duration) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, error) {
	if cloudAuth == nil || cloudAuth.Token == nil {
		return nil, nil, nil, fmt.Errorf("no cloud auth provided")
	}

	if cloudURL == "" {
		cloudURL = os.Getenv("DAGGER_CLOUD_URL")
	}
	if cloudURL == "" {
		cloudURL = "https://api.dagger.cloud"
	}
	cloudEndpoint, err := url.Parse(cloudURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("bad cloud URL: %w", err)
	}

	headers := map[string]string{}
	if cloudAuth.Org != nil {
		headers["X-Dagger-Org"] = cloudAuth.Org.ID
	}
	var tokenSource *sharedTokenSource
	if cloudAuthHasStaticHeader(cloudAuth) {
		headers["Authorization"] = cloudAuthHeader(cloudAuth)
	} else {
		tokenSource = &sharedTokenSource{token: cloudAuth.Token, refresh: tokenRefreshFn}
	}
	httpClient := func(sequencer *exportSequencer) *http.Client {
		client := sequencer.httpClient()
		client.Timeout = requestTimeout
		if tokenSource != nil {
			client.Transport = cloudTokenTransport{source: tokenSource, base: client.Transport}
		}
		return client
	}

	spanSequencer := newExportSequencer()
	logSequencer := newExportSequencer()
	metricSequencer := newExportSequencer()

	spanExporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(cloudEndpoint.JoinPath("v1", "traces").String()),
		otlptracehttp.WithHeaders(headers),
		otlptracehttp.WithHTTPClient(httpClient(spanSequencer)))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure cloud tracing: %w", err)
	}
	logExporter, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpointURL(cloudEndpoint.JoinPath("v1", "logs").String()),
		otlploghttp.WithHeaders(headers),
		otlploghttp.WithHTTPClient(httpClient(logSequencer)))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure cloud logging: %w", err)
	}
	metricExporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(cloudEndpoint.JoinPath("v1", "metrics").String()),
		otlpmetrichttp.WithHeaders(headers),
		otlpmetrichttp.WithHTTPClient(httpClient(metricSequencer)))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure cloud metrics: %w", err)
	}

	return NewSpanHeartbeater(&sequencedSpanExporter{sequencer: spanSequencer, exporter: spanExporter}),
		&sequencedLogExporter{sequencer: logSequencer, exporter: logExporter},
		&sequencedMetricExporter{sequencer: metricSequencer, exporter: metricExporter},
		nil
}

// cloudAuthHasStaticHeader reports whether the credential is sent as a fixed
// Authorization header: an engine token or an OIDC token. Otherwise it is an
// OAuth access token that expires.
func cloudAuthHasStaticHeader(ca *auth.Cloud) bool {
	return ca.Token.TokenType == "Basic" || ca.Token.TokenType == "OIDC"
}

// cloudAuthHeader converts a Cloud credential into an HTTP Authorization
// header value: engine tokens as HTTP basic auth with the token as user name,
// OIDC tokens as a bearer JWT.
func cloudAuthHeader(ca *auth.Cloud) string {
	switch ca.Token.TokenType {
	case "Basic":
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(ca.Token.AccessToken+":"))
	case "OIDC":
		return "Bearer " + ca.Token.AccessToken
	default:
		return ca.Token.Type() + " " + ca.Token.AccessToken
	}
}

// CloudTokenRefreshTimeout bounds one refresh of an expired OAuth token.
const CloudTokenRefreshTimeout = 5 * time.Second

// BoundedTokenRefresh bounds each call of a token refresh callback by
// CloudTokenRefreshTimeout, on a context of its own, since exports refresh
// from background goroutines long after any request.
func BoundedTokenRefresh(refresh func(context.Context) (*oauth2.Token, error)) func(context.Context) (*oauth2.Token, error) {
	return boundedTokenRefresh(refresh, CloudTokenRefreshTimeout)
}

func boundedTokenRefresh(refresh func(context.Context) (*oauth2.Token, error), timeout time.Duration) func(context.Context) (*oauth2.Token, error) {
	return func(context.Context) (*oauth2.Token, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return refresh(ctx)
	}
}

// cloudRefreshBackoff is how long a failed refresh answers for itself: a
// queue of waiting exports does not become a queue of refreshes.
const cloudRefreshBackoff = time.Second

// sharedTokenSource hands the exporters of one credential its current OAuth
// token. An expired token is refreshed with at most one refresh in flight:
// concurrent callers wait for its result or for their own context, and a
// caller whose context has already ended never starts one. A failed refresh
// is not retried for cloudRefreshBackoff. With no refresh callback the token
// is used as it is.
type sharedTokenSource struct {
	refresh func(context.Context) (*oauth2.Token, error)

	mu       sync.Mutex
	token    *oauth2.Token
	inflight chan struct{} // closed when the refresh in flight ends
	failed   time.Time
	err      error
}

func (s *sharedTokenSource) Token(ctx context.Context) (*oauth2.Token, error) {
	for {
		s.mu.Lock()
		if s.refresh == nil || s.token.Valid() {
			token := s.token
			s.mu.Unlock()
			return token, nil
		}
		if !s.failed.IsZero() && time.Since(s.failed) < cloudRefreshBackoff {
			err := s.err
			s.mu.Unlock()
			return nil, err
		}
		if s.inflight == nil {
			if err := context.Cause(ctx); err != nil {
				s.mu.Unlock()
				return nil, err
			}
			done := make(chan struct{})
			s.inflight = done
			// The refresh carries its own bound (BoundedTokenRefresh) and
			// outlives the request that started it, so waiters can use it.
			go func() {
				token, err := s.refresh(context.Background())
				s.mu.Lock()
				if err == nil {
					s.token, s.failed, s.err = token, time.Time{}, nil
				} else {
					s.failed, s.err = time.Now(), err
				}
				s.inflight = nil
				s.mu.Unlock()
				close(done)
			}()
		}
		wait := s.inflight
		s.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
}

// cloudTokenTransport sets the current OAuth token on each request, like
// oauth2.Transport, but waits for a refresh only as long as the request's
// context allows, so an export's timeout or cancellation ends the wait while
// a refresh stalls.
type cloudTokenTransport struct {
	source *sharedTokenSource
	base   http.RoundTripper
}

func (t cloudTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.source.Token(req.Context())
	if err != nil {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, err
	}
	authorized := req.Clone(req.Context())
	authorized.Header = req.Header.Clone()
	token.SetAuthHeader(authorized)
	return t.base.RoundTrip(authorized)
}
