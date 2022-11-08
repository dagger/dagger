package http

import (
	"context"
	"net"
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

func (c *HTTP) Connect(ctx context.Context, cfg *engineconn.Config) (engineconn.Dialer, error) {
	return func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", c.u.Host)
	}, nil
}

func (c *HTTP) Close() error {
	return nil
}
