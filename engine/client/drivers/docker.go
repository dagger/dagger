package drivers

import (
	"context"
	"io"
	"net"
	"os/exec"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/util/traceexec"
	"github.com/docker/cli/cli/connhelper/commandconn"
)

func init() {
	register("docker-image", &imageDriver{docker{cmd: "docker"}, imageload.Docker{Cmd: "docker"}})
	register("docker-container", &containerDriver{docker{cmd: "docker"}, imageload.Docker{Cmd: "docker"}})

	register("nerdctl-image", &imageDriver{docker{cmd: "nerdctl"}, imageload.Docker{Cmd: "nerdctl"}})
	register("nerdctl-container", &containerDriver{docker{cmd: "nerdctl"}, imageload.Docker{Cmd: "nerdctl"}})
	register("finch-image", &imageDriver{docker{cmd: "finch"}, imageload.Docker{Cmd: "finch"}})
	register("finch-container", &containerDriver{docker{cmd: "finch"}, imageload.Docker{Cmd: "finch"}})

	register("podman-image", &imageDriver{docker{cmd: "podman"}, imageload.Docker{Cmd: "podman"}})
	register("podman-container", &containerDriver{docker{cmd: "podman"}, imageload.Docker{Cmd: "podman"}})
}

// docker is an implementation of the containerBackend for any
// docker-compatible cli.
type docker struct {
	cmd string
}

var _ containerBackend = docker{}

func (d docker) ImagePull(ctx context.Context, image string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, d.cmd, "pull", image))
}

func (d docker) ImageLoad(ctx context.Context, name string, tarball io.Reader) error {
	cmd := exec.CommandContext(ctx, d.cmd, "load")
	cmd.Stdin = tarball
	return traceexec.Exec(ctx, cmd)
}

func (d docker) ImageExists(ctx context.Context, image string) (bool, error) {
	cmd := exec.CommandContext(ctx, d.cmd, "image", "inspect", image, "--format", "{{ .ID }}")
	stdout, _, err := traceexec.ExecOutput(ctx, cmd, telemetry.Encapsulated())
	if err == nil {
		return true, nil
	}
	if stdout == "[]" {
		return false, nil
	}
	return false, err
}

func (d docker) ImageTag(ctx context.Context, dest string, src string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, d.cmd, "tag", src, dest))
}

func (d docker) ContainerRun(ctx context.Context, name string, opts runOpts) error {
	args := []string{"run", "--name", name, "-d"}
	for _, volume := range opts.volumes {
		args = append(args, "-v", volume)
	}
	for _, env := range opts.env {
		args = append(args, "-e", env)
	}
	for _, port := range opts.ports {
		args = append(args, "-p", port)
	}
	if opts.privileged {
		args = append(args, "--privileged")
	}
	if opts.gpus {
		args = append(args, "--gpus", "all")
	}

	args = append(args, opts.image)
	args = append(args, opts.args...)

	return traceexec.Exec(ctx, exec.CommandContext(ctx, d.cmd, args...))
}

func (d docker) ContainerDial(ctx context.Context, name string, args []string) (net.Conn, error) {
	cmdArgs := append([]string{"exec", "-i", name}, args...)
	return commandconn.New(ctx, d.cmd, cmdArgs...)
}

func (d docker) ContainerRemove(ctx context.Context, name string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, d.cmd, "rm", "-fv", name))
}

func (d docker) ContainerStart(ctx context.Context, name string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, d.cmd, "start", name))
}

func (d docker) ContainerExists(ctx context.Context, name string) (bool, error) {
	cmd := exec.CommandContext(ctx, d.cmd, "container", "inspect", name, "--format", "{{ .ID }}")
	_, stderr, err := traceexec.ExecOutput(ctx, cmd, telemetry.Encapsulated())
	if err == nil {
		return true, nil
	}
	if strings.Contains(strings.ToLower(stderr), "no such container") {
		return false, nil
	}
	return false, err
}

func (d docker) ContainerLs(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, d.cmd, "ps", "-a", "--format", "{{.Names}}")
	stdout, _, err := traceexec.ExecOutput(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if stdout == "" {
		return nil, err
	}
	return strings.Split(stdout, "\n"), nil
}
