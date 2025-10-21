package drivers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/util/traceexec"
	"github.com/docker/cli/cli/connhelper/commandconn"
)

type apple struct{}

var _ containerBackend = apple{}

func (apple) Available(ctx context.Context) (bool, error) {
	// check binary exists
	if _, err := exec.LookPath("container"); err != nil {
		return false, nil //nolint:nilerr
	}

	// check daemon is running
	cmd := exec.CommandContext(ctx, "container", "system", "status")
	if err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated()); err != nil {
		return false, err
	}
	return true, nil
}

func (apple) ImagePull(ctx context.Context, image string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, "container", "image", "pull", image))
}

func (apple) ImageExists(ctx context.Context, image string) (bool, error) {
	cmd := exec.CommandContext(ctx, "container", "image", "inspect", image)
	err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated())
	return err == nil, nil
}

func (apple) ImageRemove(ctx context.Context, image string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, "container", "image", "rm", image))
}

func (apple) ImageLoader(ctx context.Context) imageload.Backend {
	return imageload.Apple{}
}

func (apple) ContainerRun(ctx context.Context, name string, opts runOpts) error {
	args := []string{"run", "--name", name, "-d"}

	envs := os.Environ()

	for _, volume := range opts.volumes {
		if !strings.Contains(volume, ":") {
			// skip anonymous volumes, container doesn't support them
			continue
		}
		args = append(args, "-v", volume)
	}
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
		return fmt.Errorf("unsupported port argument %q", port)
	}

	if opts.cpus != "" {
		args = append(args, "--cpus", opts.cpus)
	} else {
		// default is 2 CPUs, not generally enough for the engine
		args = append(args, "--cpus", "4")
	}
	if opts.memory != "" {
		args = append(args, "--memory", opts.memory)
	} else {
		// default is 2 G, *definitely* not enough for the engine
		args = append(args, "--memory", "8G")
	}

	args = append(args, opts.image)
	args = append(args, opts.args...)

	cmd := exec.CommandContext(ctx, "container", args...)
	cmd.Env = envs
	_, stderr, err := traceexec.ExecOutput(ctx, cmd)
	if err != nil {
		if isAppleContainerAlreadyInUseOutput(stderr) {
			return errContainerAlreadyExists
		}
		return err
	}
	return nil
}

func (apple) ContainerExec(ctx context.Context, name string, args []string) (string, string, error) {
	cmdArgs := append([]string{"exec", name}, args...)
	return traceexec.ExecOutput(ctx, exec.CommandContext(ctx, "container", cmdArgs...))
}

func (apple) ContainerDial(ctx context.Context, name string, args []string) (net.Conn, error) {
	cmdArgs := append([]string{"exec", "-i", name}, args...)
	return commandconn.New(ctx, "container", cmdArgs...)
}

func (apple) ContainerRemove(ctx context.Context, name string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, "container", "rm", "-f", name))
}

func (a apple) ContainerStart(ctx context.Context, name string) error {
	// TODO: remove is running check when apple container will stop removing running container
	// When doing a 'container start' on an existing running container, apple container
	// will fail to start it. That's expected as the container is already running.
	// But when an error occurs, apple container will stop and remove the container.
	// That way, to 'container start' an already running container will stop and remove it.
	if running, err := a.ContainerIsRunning(ctx, name); err == nil && running {
		return nil
	}
	return traceexec.Exec(ctx, exec.CommandContext(ctx, "container", "start", name), telemetry.Encapsulated())
}

func (apple) ContainerExists(ctx context.Context, name string) (bool, error) {
	cmd := exec.CommandContext(ctx, "container", "inspect", name)
	err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated())
	return err == nil, nil
}

func (apple) ContainerIsRunning(ctx context.Context, name string) (bool, error) {
	cmd := exec.CommandContext(ctx, "container", "ls", "-a", "--format", "json")
	stdout, _, err := traceexec.ExecOutput(ctx, cmd)
	if err != nil {
		return false, err
	}

	var result []struct {
		Status        string `json:"status"`
		Configuration struct {
			ID string `json:"id"`
		} `json:"configuration"`
	}
	err = json.Unmarshal([]byte(stdout), &result)
	if err != nil {
		return false, err
	}
	for _, res := range result {
		if res.Configuration.ID == name && res.Status == "running" {
			return true, nil
		}
	}
	return false, nil
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

func isAppleContainerAlreadyInUseOutput(output string) bool {
	return strings.Contains(output, "already exists")
}
