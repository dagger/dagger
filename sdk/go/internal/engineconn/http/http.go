package http

import (
	"context"
	"net/http"
	"net/url"

	"dagger.io/dagger/internal/engineconn"
)

func init() {
	engineconn.Register("http", New)
}

var _ engineconn.EngineConn = &HTTP{}

type HTTP struct {
	u *url.URL
}

func New(u *url.URL) (engineconn.EngineConn, error) {
	return &HTTP{
		u: u,
	}, nil
}

func (c *HTTP) Addr() string {
	return c.u.String()
}

func (c *HTTP) Connect(ctx context.Context, cfg *engineconn.Config) (*http.Client, error) {
	return &http.Client{}, nil
}

func (c *HTTP) Close() error {
	return nil
}
