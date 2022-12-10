package dockerprovision

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"

	"dagger.io/dagger/internal/engineconn"
	"dagger.io/dagger/internal/engineconn/bin"
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
	tmpbinName := "temp-dagger-" + c.containerName + "*"
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
		return nil, errors.Wrapf(err, "failed to copy dagger bin: %s", output)
	}

	if err := tmpbin.Chmod(0o700); err != nil {
		return nil, err
	}

	if err := tmpbin.Close(); err != nil {
		return nil, err
	}

	args := []string{"session"}
	if cfg.Workdir != "" {
		args = append(args, "--workdir", cfg.Workdir)
	}
	if cfg.ConfigPath != "" {
		args = append(args, "--project", cfg.ConfigPath)
	}

	defaultDaggerRunnerHost := "docker-container://" + c.containerName
	httpClient, childStdin, err := bin.StartEngineSession(ctx, cfg.LogOutput, defaultDaggerRunnerHost, tmpbin.Name(), args...)
	if err != nil {
		return nil, err
	}
	c.childStdin = childStdin

	return httpClient, nil
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
