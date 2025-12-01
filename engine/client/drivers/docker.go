package drivers

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/util/traceexec"
	"github.com/docker/cli/cli/connhelper/commandconn"
)

// docker is an implementation of the containerBackend for any
// docker-compatible cli.
type docker struct {
	cmd string
}

var _ containerBackend = docker{}

func (d docker) Available(ctx context.Context) (bool, error) {
	// check binary exists
	if _, err := exec.LookPath(d.cmd); err != nil {
		return false, nil //nolint:nilerr
	}

	// check daemon is running
	cmd := exec.CommandContext(ctx, d.cmd, "version")
	if err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated()); err != nil {
		return false, err
	}
	return true, nil
}

func (d docker) ImagePull(ctx context.Context, image string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, d.cmd, "pull", image))
}

func (d docker) ImageExists(ctx context.Context, image string) (bool, error) {
	cmd := exec.CommandContext(ctx, d.cmd, "image", "inspect", image, "--format", "{{ .ID }}")
	_, stderr, err := traceexec.ExecOutput(ctx, cmd, telemetry.Encapsulated())
	if err == nil {
		return true, nil
	}

	stderr = strings.ToLower(stderr)
	if strings.Contains(stderr, "no such image") || strings.Contains(stderr, "image not known") {
		return false, nil
	}
	return false, err
}

func (d docker) ImageRemove(ctx context.Context, image string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, d.cmd, "image", "rm", image))
}

func (d docker) ImageLoader(ctx context.Context) imageload.Backend {
	return imageload.Docker{Cmd: d.cmd}
}

func (d docker) ContainerRun(ctx context.Context, name string, opts runOpts) error {
	args := []string{"run",
		"--name", name,
		"-d",
		"--restart", "always", // load-bearing to prevent https://github.com/dagger/dagger/issues/7785 from being fatal
	}
	for _, volume := range opts.volumes {
		args = append(args, "-v", volume)
	}

	envs := os.Environ()

	for _, env := range opts.env {
		k, _, ok := strings.Cut(env, "=")
		if ok {
			args = append(args, "-e", k)
			envs = append(envs, env)
		} else {
			args = append(args, "-e", env)
		}
	}
	for _, port := range opts.ports {
		args = append(args, "-p", port)
	}
	if opts.privileged {
		args = append(args, "--privileged")
	}

	if opts.cpus != "" {
		args = append(args, "--cpus", opts.cpus)
	}
	if opts.memory != "" {
		args = append(args, "--memory", opts.memory)
	}
	if opts.gpus {
		args = append(args, "--gpus", "all")
	}

	args = append(args, opts.image)
	args = append(args, opts.args...)

	cmd := exec.CommandContext(ctx, d.cmd, args...)
	cmd.Env = envs
	_, stderr, err := traceexec.ExecOutput(ctx, cmd)
	if err != nil {
		if isContainerAlreadyInUseOutput(stderr) {
			return errContainerAlreadyExists
		}
		return err
	}
	return nil
}

func (d docker) ContainerExec(ctx context.Context, name string, args []string) (string, string, error) {
	cmdArgs := append([]string{"exec", name}, args...)
	return traceexec.ExecOutput(ctx, exec.CommandContext(ctx, d.cmd, cmdArgs...))
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

func (d docker) ContainerLs(ctx context.Context) ([]container, error) {
	cmd := exec.CommandContext(ctx, d.cmd, "ps", "-a", "--format", "{{.Names}} {{.State}}")
	stdout, _, err := traceexec.ExecOutput(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if stdout == "" {
		return nil, err
	}
	lines := strings.Split(stdout, "\n")
	containers := make([]container, len(lines))
	for i, line := range lines {
		parts := strings.SplitN(line, " ", 2)
		containers[i].name = parts[0]
		if len(parts) == 2 {
			containers[i].running = parts[1] == "running"
		}
	}
	return containers, nil
}

func isContainerAlreadyInUseOutput(output string) bool {
	switch {
	case strings.Contains(output, "is already in use"):
		// docker/podman cli output
		return true
	case strings.Contains(output, "is already used"):
		// nerdctl cli output
		return true
	}
	return false
}
