package dockerprovision

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"dagger.io/dagger/internal/engineconn"
	"dagger.io/dagger/internal/engineconn/bin"
	"github.com/adrg/xdg"
	"github.com/opencontainers/go-digest"
	exec "golang.org/x/sys/execabs"
)

var _ engineconn.EngineConn = &DockerImage{}

func NewDockerImage(u *url.URL) (engineconn.EngineConn, error) {
	return &DockerImage{
		imageRef: u.Host + u.Path,
	}, nil
}

type DockerImage struct {
	imageRef   string
	childStdin io.Closer
}

func (c *DockerImage) Connect(ctx context.Context, cfg *engineconn.Config) (*http.Client, error) {
	// TODO: does xdg work on Windows?
	cacheDir := filepath.Join(xdg.CacheHome, "dagger")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, err
	}

	// NOTE: this isn't as robust as using the official docker parser, but
	// our other SDKs don't have access to that, so this is simpler to
	// replicate and keep consistent.
	var id string
	_, dgst, ok := strings.Cut(c.imageRef, "@sha256:")
	if !ok {
		return nil, fmt.Errorf("invalid image reference %q", c.imageRef)
	}
	if err := digest.Digest("sha256:" + dgst).Validate(); err != nil {
		return nil, fmt.Errorf("invalid digest: %w", err)
	}
	id = dgst
	id = id[:hashLen]

	engineSessionBinName := daggerCLIBinPrefix + id
	if runtime.GOOS == "windows" {
		engineSessionBinName += ".exe"
	}
	engineSessionBinPath := filepath.Join(cacheDir, engineSessionBinName)

	if _, err := os.Stat(engineSessionBinPath); os.IsNotExist(err) {
		tmpbin, err := os.CreateTemp(cacheDir, "temp-"+engineSessionBinName)
		if err != nil {
			return nil, fmt.Errorf("failed to create temp file: %w", err)
		}
		defer tmpbin.Close()
		defer os.Remove(tmpbin.Name())

		dockerRunArgs := []string{
			"docker", "run",
			"--rm",
			"--entrypoint", "/bin/cat",
			c.imageRef,
			containerEngineSessionBinPrefix + runtime.GOOS + "-" + runtime.GOARCH,
		}
		// #nosec
		cmd := exec.CommandContext(ctx, dockerRunArgs[0], dockerRunArgs[1:]...)
		cmd.Stdout = tmpbin
		if cfg.LogOutput != nil {
			cmd.Stderr = cfg.LogOutput
		}
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("failed to transfer dagger bin with command %q: %w", strings.Join(dockerRunArgs, " "), err)
		}

		if err := tmpbin.Chmod(0o700); err != nil {
			return nil, err
		}

		if err := tmpbin.Close(); err != nil {
			return nil, fmt.Errorf("failed to close temporary file: %w", err)
		}

		// TODO: verify checksum?
		// Cache the bin for future runs.
		if err := os.Rename(tmpbin.Name(), engineSessionBinPath); err != nil {
			return nil, fmt.Errorf("failed to rename %q to %q: %w", tmpbin.Name(), engineSessionBinPath, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("failed to stat %q: %w", engineSessionBinPath, err)
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if cfg.LogOutput != nil {
			fmt.Fprintf(cfg.LogOutput, "failed to list cache dir: %v", err)
		}
	} else {
		for _, entry := range entries {
			if entry.Name() == engineSessionBinName {
				continue
			}
			if strings.HasPrefix(entry.Name(), daggerCLIBinPrefix) {
				if err := os.Remove(filepath.Join(cacheDir, entry.Name())); err != nil {
					if cfg.LogOutput != nil {
						fmt.Fprintf(cfg.LogOutput, "failed to remove old dagger bin: %v", err)
					}
				}
			}
		}
	}

	args := []string{"session"}
	if cfg.Workdir != "" {
		args = append(args, "--workdir", cfg.Workdir)
	}
	if cfg.ConfigPath != "" {
		args = append(args, "--project", cfg.ConfigPath)
	}

	defaultDaggerRunnerHost := "docker-image://" + c.imageRef
	httpClient, childStdin, err := bin.StartEngineSession(ctx, cfg.LogOutput, defaultDaggerRunnerHost, engineSessionBinPath, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to start dagger bin: %w", err)
	}
	c.childStdin = childStdin

	return httpClient, nil
}

func (c *DockerImage) Addr() string {
	return "http://dagger"
}

func (c *DockerImage) Close() error {
	if c.childStdin != nil {
		return c.childStdin.Close()
	}
	return nil
}
