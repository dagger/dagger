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

func (c *Unix) Connect(ctx context.Context, cfg *engineconn.Config) (*http.Client, error) {
	// FIXME: These are necessary for dagger-in-dagger but do not work.
	// if cfg.Workdir != "" {
	// 	return nil, errors.New("workdir not supported on unix hosts")
	// }
	// if cfg.ConfigPath != "" {
	// 	return nil, errors.New("config path not supported on unix hosts")
	// }
	// if cfg.NoExtensions {
	// 	return nil, errors.New("no extensions is not supported on unix hosts")
	// }
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", c.path)
			},
		},
	}, nil
}

func (c *Unix) Close() error {
	return nil
}
