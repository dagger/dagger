package unix

import (
	"context"
	"net"
	"net/http"
	"net/url"

	"dagger.io/dagger/internal/engineconn"
)

func init() {
	engineconn.Register("unix", New)
}

var _ engineconn.EngineConn = &Unix{}

type Unix struct {
	u *url.URL
}

func New(u *url.URL) (engineconn.EngineConn, error) {
	return &Unix{
		u: u,
	}, nil
}

func (c *Unix) Addr() string {
	return "http://dagger"
}

func (c *Unix) Connect(ctx context.Context, cfg *engineconn.Config) (*http.Client, error) {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", c.u.Host+c.u.Path)
			},
		},
	}, nil
}

func (c *Unix) Close() error {
	return nil
}
