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
	// trim image digests to 16 characters to makeoutput more readable
	digestLen           = 16
	containerNamePrefix = "dagger-engine-"
	helperBinPrefix     = "dagger-sdk-helper-"
)

var _ engineconn.EngineConn = &DockerProvision{}

type DockerProvision struct {
	imageRef   string
	childStdin io.Closer
}

func New(u *url.URL) (engineconn.EngineConn, error) {
	return &DockerProvision{
		imageRef: u.Host + u.Path,
	}, nil
}

func (c *DockerProvision) Connect(ctx context.Context, cfg *engineconn.Config) (*http.Client, error) {
	// TODO: does xdg work on Windows?
	cacheDir := filepath.Join(xdg.CacheHome, "dagger")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, err
	}

	// NOTE: this isn't as robust as using the official docker parser, but
	// our other SDKs don't have access to that, so this is simpler to
	// replicate and keep consistent.
	var id string
	_, dgst, ok := strings.Cut(c.imageRef, "@sha256:")
	if !ok {
		return nil, errors.Errorf("invalid image reference %q", c.imageRef)
	}
	if err := digest.Digest("sha256:" + dgst).Validate(); err != nil {
		return nil, errors.Wrap(err, "invalid digest")
	}
	id = dgst
	id = id[:digestLen]

	helperBinName := helperBinPrefix + id
	containerName := containerNamePrefix + id
	helperBinPath := filepath.Join(cacheDir, helperBinName)

	if output, err := exec.CommandContext(ctx,
		"docker", "run",
		"--name", containerName,
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

	if output, err := exec.CommandContext(ctx,
		"docker", "ps",
		"-a",
		"--no-trunc",
		"--filter", "name=^/"+containerNamePrefix,
		"--format", "{{.Names}}",
	).CombinedOutput(); err != nil {
		// TODO: should just be debug log, but that concept doesn't exist yet
		fmt.Fprintf(os.Stderr, "failed to list containers: %s", output)
	} else {
		for _, line := range strings.Split(string(output), "\n") {
			if line == "" {
				continue
			}
			if line == containerName {
				continue
			}
			if output, err := exec.CommandContext(ctx,
				"docker", "rm", "-fv", line,
			).CombinedOutput(); err != nil {
				// TODO: should just be debug log, but that concept doesn't exist yet
				fmt.Fprintf(os.Stderr, "failed to remove old container %s: %s", line, output)
			}
		}
	}

	if _, err := os.Stat(helperBinPath); os.IsNotExist(err) {
		tmpbin, err := os.CreateTemp(cacheDir, "temp-"+helperBinName)
		if err != nil {
			return nil, err
		}
		defer tmpbin.Close()
		defer os.Remove(tmpbin.Name())

		if output, err := exec.CommandContext(ctx,
			"docker", "cp",
			containerName+":/usr/bin/dagger-sdk-helper-"+runtime.GOOS+"-"+runtime.GOARCH,
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
		// Cache the bin for future runs.
		if err := os.Rename(tmpbin.Name(), helperBinPath); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list cache dir: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == helperBinName {
			continue
		}
		if strings.HasPrefix(entry.Name(), helperBinPrefix) {
			if err := os.Remove(filepath.Join(cacheDir, entry.Name())); err != nil {
				return nil, fmt.Errorf("failed to remove old helper bin: %w", err)
			}
		}
	}

	remote := "docker-container://" + containerName

	args := []string{
		"--remote", remote,
	}
	if cfg.Workdir != "" {
		args = append(args, "--workdir", cfg.Workdir)
	}
	if cfg.ConfigPath != "" {
		args = append(args, "--project", cfg.ConfigPath)
	}

	addr, err := c.startHelper(ctx, cfg.LogOutput, helperBinPath, args...)
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

func (c *DockerProvision) startHelper(ctx context.Context, stderr io.Writer, cmd string, args ...string) (string, error) {
	proc := exec.CommandContext(ctx, cmd, args...)
	proc.Env = os.Environ()
	proc.Stderr = stderr
	setPlatformOpts(proc)

	stdout, err := proc.StdoutPipe()
	if err != nil {
		return "", err
	}
	defer stdout.Close() // don't need it after we read the port

	// Open a stdin pipe with the child process. The helper shutsdown
	// when it is closed. This is a platform-agnostic way of ensuring
	// we don't leak child processes even if this process is SIGKILL'd.
	c.childStdin, err = proc.StdinPipe()
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
	} // TODO: validation its in the right range

	return fmt.Sprintf("localhost:%d", port), nil
}

func (c *DockerProvision) Close() error {
	if c.childStdin != nil {
		return c.childStdin.Close()
	}
	return nil
}
