package engineconn

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
)

type RegisterFunc func(*url.URL) (EngineConn, error)

var helpers = map[string]RegisterFunc{}

type EngineConn interface {
	Addr() string
	Connect(ctx context.Context, cfg *Config) (*http.Client, error)
	Close() error
}

func Get(host string) (EngineConn, error) {
	u, err := url.Parse(host)
	if err != nil {
		return nil, err
	}

	fn, ok := helpers[u.Scheme]
	if !ok {
		return nil, fmt.Errorf("invalid dagger host %q", host)
	}

	return fn(u)
}

type Config struct {
	Workdir      string
	ConfigPath   string
	NoExtensions bool
	LogOutput    io.Writer
}

// Register registers new connectionhelper for scheme
func Register(scheme string, fn RegisterFunc) {
	helpers[scheme] = fn
}

type ConnectParams struct {
	Host         string `json:"host"`
	SessionToken string `json:"session_token"`
}

func DefaultHTTPClient(p ConnectParams) *http.Client {
	return &http.Client{
		Transport: RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			r.SetBasicAuth(p.SessionToken, "")
			return (&http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("tcp", p.Host)
				},
			}).RoundTrip(r)
		}),
	}
}

type RoundTripperFunc func(*http.Request) (*http.Response, error)

func (f RoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
