package drivers

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"

	"github.com/dagger/dagger/util/traceexec"
)

func init() {
	register("apple-image", &imageDriver{apple{"apple"}, nil})
	register("apple-container", &containerDriver{apple{"apple"}, nil})
}

// apple is an implementation of the containerBackend for any
// apple-compatible cli.
type apple struct {
	cmd string
}

var _ containerBackend = apple{}

func (d apple) ImagePull(image string) *exec.Cmd {
	return exec.Command("container", "image", "pull", image)
}

func (d apple) ImageLoad(name string, tarball io.Reader) *exec.Cmd {
	cmd := exec.Command("container", "image", "load")
	cmd.Stdin = tarball
	return cmd
}

func (d apple) ImageInspect(image string) *exec.Cmd {
	return exec.Command("container", "image", "inspect", image)
}

func (d apple) ImageTag(dest string, src string) *exec.Cmd {
	return exec.Command("container", "image", "tag", src, dest)
}

func (d apple) ContainerRun(name string, opts runOpts) *exec.Cmd {
	// TODO: set resources for the engine with --cpus and --memory
	args := []string{"run", "--name", name, "-d"}
	for _, volume := range opts.volumes {
		if strings.Contains(volume, ":") {
			args = append(args, "-v", volume)
		}
	}
	for _, env := range opts.env {
		args = append(args, "-e", env)
	}
	for _, _ = range opts.ports {
		panic("oh shit theres a port!!")
		// args = append(args, "-p", port)
	}
	// if opts.privileged {
	// 	args = append(args, "--privileged")
	// }
	// if opts.gpus {
	// 	args = append(args, "--gpus", "all")
	// }

	args = append(args, opts.image)
	args = append(args, opts.args...)
	return exec.Command("container", args...)
}

func (d apple) ContainerExec(name string, args []string) *exec.Cmd {
	cmdArgs := append([]string{"exec", "-i", name}, args...)
	return exec.Command("container", cmdArgs...)
}

func (d apple) ContainerRemove(name string) *exec.Cmd {
	return exec.Command("container", "rm", "-f", name)
}

func (d apple) ContainerStart(name string) *exec.Cmd {
	return exec.Command("container", "start", name)
}

func (d apple) ContainerInspect(name string) *exec.Cmd {
	return exec.Command("container", "inspect", name)
}

func (d apple) ContainerLs(ctx context.Context) ([]string, error) {
	cmd := exec.Command("container", "ls", "-a", "--format", "json")

	stdout, err := traceexec.Exec(ctx, cmd)
	out := strings.TrimSpace(stdout)
	if err != nil {
		return nil, err
	}
	var result []struct {
		Configuration struct {
			ID string `json:"id"`
		} `json:"configuration"`
	}
	err = json.Unmarshal([]byte(out), &result)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, res := range result {
		ids = append(ids, res.Configuration.ID)
	}
	return ids, nil
}
