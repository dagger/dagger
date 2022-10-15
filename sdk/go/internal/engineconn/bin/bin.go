package embedded

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"dagger.io/dagger/internal/engineconn"
	"github.com/docker/cli/cli/connhelper/commandconn"
)

func init() {
	engineconn.Register("bin", New)
}

var _ engineconn.EngineConn = &Bin{}

type Bin struct {
	path string
}

func New(u *url.URL) (engineconn.EngineConn, error) {
	return &Bin{
		path: u.Host + u.Path,
	}, nil
}

func (c *Bin) Connect(ctx context.Context, cfg *engineconn.Config) (*http.Client, error) {
	args := []string{
		"dial-stdio",
	}
	if cfg.Workdir != "" {
		args = append(args, "--workdir", cfg.Workdir)
	}
	if cfg.ConfigPath != "" {
		args = append(args, "--project", cfg.ConfigPath)
	}
	for id, path := range cfg.LocalDirs {
		args = append(args, "--local-dir", fmt.Sprintf("%s=%s", id, path))
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return commandconn.New(ctx, c.path, args...)
			},
		},
	}, nil
}

func (c *Bin) Close() error {
	return nil
}
