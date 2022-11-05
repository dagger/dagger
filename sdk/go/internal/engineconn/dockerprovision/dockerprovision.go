package dockerprovision

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"dagger.io/dagger/internal/engineconn"
	"github.com/adrg/xdg"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	exec "golang.org/x/sys/execabs"
)

func init() {
	engineconn.Register("docker-provision", New)
}

const (
	// trim image digests to 16 characters to make output more readable
	digestLen = 16
)

var _ engineconn.EngineConn = &DockerProvision{}

type DockerProvision struct {
	imageRef string
}

func New(u *url.URL) (engineconn.EngineConn, error) {
	return &DockerProvision{
		imageRef: u.Host + u.Path,
	}, nil
}

func (c *DockerProvision) Connect(ctx context.Context, cfg *engineconn.Config) (*http.Client, error) {
	// TODO: also support using a cloak bin already in $PATH or a user-specified path, and a dev mode version where it's always thrown away, etc.

	// TODO: does xdg work on Windows?
	cacheDir := filepath.Join(xdg.CacheHome, "dagger")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, err
	}

	// NOTE: this isn't as robust as using the official docker parser, but
	// our other SDKs don't have access to that, so this is simpler to
	// replicate and keep consistent.
	var id string
	var isPinned bool
	if _, dgst, ok := strings.Cut(c.imageRef, "@sha256:"); ok {
		if err := digest.Digest("sha256:" + dgst).Validate(); err != nil {
			return nil, errors.Wrap(err, "invalid digest")
		}
		id = dgst[:digestLen]
		isPinned = true
	} else {
		// still hash it because chars like / are not allowed in filenames
		id = digest.FromString(c.imageRef).Encoded()[:digestLen]
	}

	helperBinPath := filepath.Join(cacheDir, "dagger-sdk-helper-"+id)
	containerName := "dagger-engine-" + id
	volumeName := "dagger-engine-" + id

	if output, err := exec.CommandContext(ctx,
		"docker", "run",
		"--name", containerName,
		"-v", volumeName+":/var/lib/buildkit",
		"-d",
		"--restart", "always",
		"--privileged",
		c.imageRef,
		"--debug",
	).CombinedOutput(); err != nil {
		if !strings.Contains(
			string(output),
			fmt.Sprintf(`Conflict. The container name "/%s" is already in use by container`, containerName),
		) {
			return nil, errors.Wrapf(err, "failed to run container: %s", output)
		}
	}

	if _, err := os.Stat(helperBinPath); os.IsNotExist(err) {
		tmpbin, err := os.CreateTemp(cacheDir, "dagger-sdk-helper")
		if err != nil {
			return nil, err
		}
		defer tmpbin.Close()
		defer os.Remove(tmpbin.Name())

		if output, err := exec.CommandContext(ctx,
			"docker", "cp",
			containerName+":/usr/bin/dagger-sdk-helper-"+runtime.GOOS,
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

		if isPinned {
			// If we're running against a pinned SHA, we want to keep this in the cache.
			// Rename succeeds even if the file already exists and is being executed.
			if err := os.Rename(tmpbin.Name(), helperBinPath); err != nil {
				return nil, err
			}
		} else {
			// Otherwise, we can unlink it once the helper has started running, which
			// results in it being cleared from the filesystem once the helper exits.
			defer os.Remove(tmpbin.Name())
		}
	} else if err != nil {
		return nil, err
	}

	buildkitHost := "docker-container://" + containerName

	args := []string{
		"--remote", buildkitHost,
	}
	if cfg.Workdir != "" {
		args = append(args, "--workdir", cfg.Workdir)
	}
	if cfg.ConfigPath != "" {
		args = append(args, "--project", cfg.ConfigPath)
	}

	addr, err := startHelper(ctx, cfg.LogOutput, helperBinPath, args...)
	if err != nil {
		return nil, err
	}

	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				// TODO: it's a little weird to totally ignore address of callback, could
				// consider refactoring engineconn interface, not sure if worth it
				return net.Dial("tcp", addr)
			},
		},
	}, nil
}

func startHelper(ctx context.Context, stderr io.Writer, cmd string, args ...string) (string, error) {
	proc := exec.CommandContext(ctx, cmd, args...)
	proc.Env = os.Environ()
	proc.Stderr = stderr
	proc.SysProcAttr = &syscall.SysProcAttr{
		// TODO: don't think this compiles on darwin or windows
		Pdeathsig: syscall.SIGKILL,
	}

	stdout, err := proc.StdoutPipe()
	if err != nil {
		return "", err
	}

	if err := proc.Start(); err != nil {
		return "", err
	}

	// Read the port to connect to from the helper's stdout.
	// TODO: timeouts and such
	portStr, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		return "", err
	}
	portStr = strings.TrimSpace(portStr)
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", err
	}
	// TODO: validation its in the right range

	return fmt.Sprintf("localhost:%d", port), nil
}

func (c *DockerProvision) Close() error {
	return nil
}
