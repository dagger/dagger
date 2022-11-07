package unix

import (
	"context"
	"net"
	"net/url"

	"dagger.io/dagger/internal/engineconn"
)

func init() {
	engineconn.Register("tcp", New)
}

var _ engineconn.EngineConn = &TCP{}

type TCP struct {
	addr string
}

func New(u *url.URL) (engineconn.EngineConn, error) {
	return &TCP{
		addr: u.Host,
	}, nil
}

func (c *TCP) Connect(ctx context.Context, cfg *engineconn.Config) (engineconn.Dialer, error) {
	return func(_ context.Context) (net.Conn, error) {
		return net.Dial("tcp", c.addr)
	}, nil
}

func (c *TCP) Close() error {
	return nil
}
