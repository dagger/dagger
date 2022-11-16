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
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	exec "golang.org/x/sys/execabs"
)

func NewDockerContainer(u *url.URL) (engineconn.EngineConn, error) {
	containerName := u.Host + u.Path
	if containerName == "" {
		return nil, errors.New("container name must be specified")
	}
	return &DockerContainer{
		containerName: containerName,
	}, nil
}

type DockerContainer struct {
	containerName string
	childStdin    io.Closer
	tmpbinPath    string
}

func (c *DockerContainer) Connect(ctx context.Context, cfg *engineconn.Config) (*http.Client, error) {
	tmpbinName := "temp-dagger-engine-session" + c.containerName + "*"
	if runtime.GOOS == "windows" {
		tmpbinName += ".exe"
	}
	tmpbin, err := os.CreateTemp("", tmpbinName)
	if err != nil {
		return nil, err
	}
	defer tmpbin.Close()
	// Don't do the clever thing and unlink after child proc starts, that doesn't work as expected on macos.
	// Instead just try to delete this in our Close() method.
	c.tmpbinPath = tmpbin.Name()

	// #nosec
	if output, err := exec.CommandContext(ctx,
		"docker", "cp",
		c.containerName+":"+containerEngineSessionBinPrefix+runtime.GOOS+"-"+runtime.GOARCH,
		tmpbin.Name(),
	).CombinedOutput(); err != nil {
		return nil, errors.Wrapf(err, "failed to copy dagger-engine-session bin: %s", output)
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

	addr, childStdin, err := startEngineSession(ctx, cfg.LogOutput, tmpbin.Name(), args...)
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
	var merr *multierror.Error
	if c.childStdin != nil {
		if err := c.childStdin.Close(); err != nil {
			merr = multierror.Append(merr, err)
		}
	}
	if c.tmpbinPath != "" {
		if err := os.Remove(c.tmpbinPath); err != nil {
			merr = multierror.Append(merr, err)
		}
	}
	return merr.ErrorOrNil()
}
