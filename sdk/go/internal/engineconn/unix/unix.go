package unix

import (
	"context"
	"net"
	"net/url"

	"dagger.io/dagger/internal/engineconn"
)

func init() {
	engineconn.Register("unix", New)
}

var _ engineconn.EngineConn = &Unix{}

type Unix struct {
	path string
}

func New(u *url.URL) (engineconn.EngineConn, error) {
	return &Unix{
		path: u.Path,
	}, nil
}

func (c *Unix) Addr() string {
	return "http://dagger"
}

func (c *Unix) Connect(ctx context.Context, cfg *engineconn.Config) (engineconn.Dialer, error) {
	return func(_ context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", c.path)
	}, nil
}

func (c *Unix) Close() error {
	return nil
}
