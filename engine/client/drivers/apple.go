package drivers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/util/traceexec"
	"github.com/docker/cli/cli/connhelper/commandconn"
)

func init() {
	register("apple-image", &imageDriver{apple{}, nil})
	register("apple-container", &containerDriver{apple{}, nil})
}

// apple is an implementation of the containerBackend for any
// apple-compatible cli.
type apple struct{}

var _ containerBackend = apple{}

func (apple) ImagePull(ctx context.Context, image string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, "container", "image", "pull", image))
}

func (apple) ImageLoad(ctx context.Context, name string, tarball io.Reader) error {
	cmd := exec.CommandContext(ctx, "container", "image", "load")
	cmd.Stdin = tarball
	return cmd.Run()
}

func (apple) ImageExists(ctx context.Context, image string) (bool, error) {
	cmd := exec.CommandContext(ctx, "container", "image", "inspect", image)
	err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated())
	return err == nil, nil
}

func (apple) ImageTag(ctx context.Context, dest string, src string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, "container", "image", "tag", src, dest))
}

func (apple) ContainerRun(ctx context.Context, name string, opts runOpts) error {
	// TODO: set resources for the engine with --cpus and --memory
	args := []string{"run", "--name", name, "-d"}
	for _, volume := range opts.volumes {
		if !strings.Contains(volume, ":") {
			// skip anonymous volumes, container doesn't support them
			continue
		}
		args = append(args, "-v", volume)
	}
	for _, env := range opts.env {
		args = append(args, "-e", env)
	}
	for _, port := range opts.ports {
		return fmt.Errorf("unsupported port argument %q", port)
	}

	args = append(args, opts.image)
	args = append(args, opts.args...)

	return traceexec.Exec(ctx, exec.CommandContext(ctx, "container", args...))
}

func (apple) ContainerDial(ctx context.Context, name string, args []string) (net.Conn, error) {
	cmdArgs := append([]string{"exec", "-i", name}, args...)
	return commandconn.New(ctx, "container", cmdArgs...)
}

func (apple) ContainerRemove(ctx context.Context, name string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, "container", "rm", "-f", name))
}

func (apple) ContainerStart(ctx context.Context, name string) error {
	// XXX: apple container returns 'running' instead of 'created'
	return traceexec.Exec(ctx, exec.CommandContext(ctx, "container", "start", name))
}

func (apple) ContainerExists(ctx context.Context, name string) (bool, error) {
	cmd := exec.CommandContext(ctx, "container", "inspect", name)
	err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated())
	return err == nil, nil
}

func (apple) ContainerLs(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "container", "ls", "-a", "--format", "json")
	stdout, _, err := traceexec.ExecOutput(ctx, cmd)
	if err != nil {
		return nil, err
	}

	var result []struct {
		Configuration struct {
			ID string `json:"id"`
		} `json:"configuration"`
	}
	err = json.Unmarshal([]byte(stdout), &result)
	if err != nil {
		return nil, err
	}

	var ids []string
	for _, res := range result {
		ids = append(ids, res.Configuration.ID)
	}
	return ids, nil
}
