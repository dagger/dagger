package engineconn

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/Khan/genqlient/graphql"
)

type EngineConn interface {
	graphql.Doer
	Host() string
	Close() error
}

type Config struct {
	Workdir      string
	ConfigPath   string
	NoExtensions bool
	LogOutput    io.Writer
}

type ConnectParams struct {
	Port         int    `json:"port"`
	SessionToken string `json:"session_token"`
}

func Get(ctx context.Context, cfg *Config) (EngineConn, error) {
	// Prefer DAGGER_SESSION_PORT if set
	conn, ok, err := FromSessionEnv()
	if err != nil {
		return nil, err
	}
	if ok {
		return conn, nil
	}

	// Try _EXPERIMENTAL_DAGGER_CLI_BIN next
	conn, ok, err = FromLocalCLI(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if ok {
		return conn, nil
	}

	// Fallback to downloading the CLI
	conn, err = FromDownloadedCLI(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func defaultHTTPClient(p *ConnectParams) *http.Client {
	return &http.Client{
		Transport: RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			r.SetBasicAuth(p.SessionToken, "")
			return (&http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", p.Port))
				},
			}).RoundTrip(r)
		}),
	}
}

type RoundTripperFunc func(*http.Request) (*http.Response, error)

func (f RoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
