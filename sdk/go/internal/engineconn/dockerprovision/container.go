package dockerprovision

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"

	"dagger.io/dagger/internal/engineconn"
	"github.com/pkg/errors"
	exec "golang.org/x/sys/execabs"
)

func NewDockerContainer(u *url.URL) (engineconn.EngineConn, error) {
	return &DockerContainer{
		containerName: u.Host + u.Path,
	}, nil
}

type DockerContainer struct {
	containerName string
	childStdin    io.Closer
}

func (c *DockerContainer) Connect(ctx context.Context, cfg *engineconn.Config) (*http.Client, error) {
	tmpbin, err := os.CreateTemp("", "temp-dagger-sdk-helper-"+c.containerName)
	if err != nil {
		return nil, err
	}
	defer tmpbin.Close()
	defer os.Remove(tmpbin.Name())

	// #nosec
	if output, err := exec.CommandContext(ctx,
		"docker", "cp",
		c.containerName+":"+containerHelperBinPrefix+runtime.GOOS+"-"+runtime.GOARCH,
		tmpbin.Name(),
	).CombinedOutput(); err != nil {
		return nil, errors.Wrapf(err, "failed to copy dagger-sdk-helper bin: %s", output)
	}

	if err := tmpbin.Chmod(0700); err != nil {
		return nil, err
	}

	if err := tmpbin.Close(); err != nil {
		return nil, err
	}

	// TODO: verify checksum?

	remote := "docker-container://" + c.containerName

	args := []string{
		"--remote", remote,
	}
	if cfg.Workdir != "" {
		args = append(args, "--workdir", cfg.Workdir)
	}
	if cfg.ConfigPath != "" {
		args = append(args, "--project", cfg.ConfigPath)
	}

	addr, childStdin, err := startHelper(ctx, cfg.LogOutput, tmpbin.Name(), args...)
	if err != nil {
		return nil, err
	}
	c.childStdin = childStdin

	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("tcp", addr)
			},
		},
	}, nil
}

func (c *DockerContainer) Addr() string {
	return "http://dagger"
}

func (c *DockerContainer) Close() error {
	if c.childStdin != nil {
		return c.childStdin.Close()
	}
	return nil
}
