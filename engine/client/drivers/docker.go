package drivers

import (
	"io"
	"os/exec"

	"github.com/dagger/dagger/engine/client/imageload"
)

func init() {
	register("docker-image", &imageDriver{docker{"docker"}, imageload.Docker{"docker"}})
	register("docker-container", &containerDriver{docker{"docker"}, imageload.Docker{"docker"}})

	register("nerdctl-image", &imageDriver{docker{"nerdctl"}, imageload.Docker{"nerdctl"}})
	register("nerdctl-container", &containerDriver{docker{"nerdctl"}, imageload.Docker{"nerdctl"}})

	register("podman-image", &imageDriver{docker{"podman"}, imageload.Docker{"podman"}})
	register("podman-container", &containerDriver{docker{"podman"}, imageload.Docker{"podman"}})
}

// docker is an implementation of the containerBackend for any
// docker-compatible cli.
type docker struct {
	cmd string
}

var _ containerBackend = docker{}

func (d docker) ImagePull(image string) *exec.Cmd {
	return exec.Command(d.cmd, "pull", image)
}

func (d docker) ImageLoad(name string, tarball io.Reader) *exec.Cmd {
	cmd := exec.Command(d.cmd, "load")
	cmd.Stdin = tarball
	return cmd
}

func (d docker) ImageInspect(image string) *exec.Cmd {
	return exec.Command(d.cmd, "image", "inspect", image)
}

func (d docker) ImageTag(dest string, src string) *exec.Cmd {
	return exec.Command(d.cmd, "tag", src, dest)
}

func (d docker) ContainerRun(name string, opts runOpts) *exec.Cmd {
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
	return exec.Command(d.cmd, args...)
}

func (d docker) ContainerExec(name string, args []string) *exec.Cmd {
	cmdArgs := append([]string{"exec", "-i", name}, args...)
	return exec.Command(d.cmd, cmdArgs...)
}

func (d docker) ContainerRemove(name string) *exec.Cmd {
	return exec.Command(d.cmd, "rm", "-fv", name)
}

func (d docker) ContainerStart(name string) *exec.Cmd {
	return exec.Command(d.cmd, "start", name)
}

func (d docker) ContainerInspect(name string) *exec.Cmd {
	return exec.Command(d.cmd, "container", "inspect", name)
}

func (d docker) ContainerLs() *exec.Cmd {
	return exec.Command(d.cmd, "ps", "-a", "--format", "{{.Names}}")
}
