package main

import (
	"context"
	"dagger/recorder/internal/dagger"
	"time"
)

type Recorder struct {
	TapesSource *dagger.Directory
	Docker      *dagger.Socket
}

func New(
	tapesSource *dagger.Directory,
	docker *dagger.Socket,
) *Recorder {
	return &Recorder{
		TapesSource: tapesSource,
		Docker:      docker,
	}
}

func (r *Recorder) Env(ctx context.Context) *dagger.Container {
	return dag.Container().
		From("ghcr.io/charmbracelet/vhs:v0.9.0").

		// Install Docker
		WithEnvVariable("DEBIAN_FRONTEND", "noninteractive").
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{"apt-get", "-y", "install", "curl", "ca-certificates", "vim", "git"}).
		WithExec([]string{"sh", "-c", "install -m 0755 -d /etc/apt/keyrings"}).
		WithExec([]string{"sh", "-c", `curl -fsSL "https://download.docker.com/linux/debian/gpg" -o /etc/apt/keyrings/docker.asc`}).
		WithExec([]string{"sh", "-c", "chmod a+r /etc/apt/keyrings/docker.asc"}).
		WithExec([]string{"sh", "-c", `echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null`}).
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{"apt-get", "-y", "install", "docker-ce-cli"}).
		WithUnixSocket("/var/run/docker.sock", r.Docker).
		WithoutEnvVariable("DEBIAN_FRONTEND").

		// Install Dagger CLI
		WithExec([]string{"sh", "-c", `curl -fsSL https://dl.dagger.io/dagger/install.sh | BIN_DIR=/usr/local/bin sh`}).

		// Initialize Dagger engine
		WithExec([]string{"dagger", "--command", ".help"}).

		// Set up work directories
		WithExec([]string{"mkdir", "/recordings"}).
		WithMountedDirectory("/tapes", r.TapesSource).
		WithEnvVariable("DO_NOT_TRACK", "1").
		WithEnvVariable("DAGGER_NO_NAG", "1").
		WithEnvVariable("CACHEBUSTER", time.Now().Format(time.RFC3339))
}

func (r *Recorder) QuickstartBasicsEnv(ctx context.Context) *dagger.Container {
	ctr := r.Env(ctx).
		WithWorkdir("/tapes/quickstart-basics")
	return ctr
}

func (r *Recorder) QuickstartBasics1(ctx context.Context) *dagger.File {
	return r.QuickstartBasicsEnv(ctx).
		WithExec([]string{"vhs", "terminal-1.tape"}).
		File("/recordings/terminal-1.gif")
}

func (r *Recorder) QuickstartBasics2(ctx context.Context) *dagger.File {
	return r.QuickstartBasicsEnv(ctx).
		WithExec([]string{"vhs", "terminal-2.tape"}).
		File("/recordings/terminal-2.gif")
}

func (r *Recorder) QuickstartBasics3(ctx context.Context) *dagger.File {
	return r.QuickstartBasicsEnv(ctx).
		WithExec([]string{"vhs", "publish-shell.tape"}).
		File("/recordings/publish-shell.gif")
}

func (r *Recorder) QuickstartBasics4(ctx context.Context) *dagger.File {
	return r.QuickstartBasicsEnv(ctx).
		WithExec([]string{"sh", "-c", "dagger init --sdk=go --name=basics"}).
		WithExec([]string{"sh", "-c", `echo 'func (m *Basics) Publish(ctx context.Context) (string, error) {\n return dag.Container().From("alpine:latest").WithNewFile("/hi.txt", "Hello from Dagger!").WithEntrypoint([]string{"cat", "/hi.txt"}).Publish(ctx, "ttl.sh/hello")  \n}' >> .dagger/main.go`}).
		WithExec([]string{"vhs", "publish-code.tape"}).
		File("/recordings/publish-code.gif")
}

func (r *Recorder) QuickstartBasics5(ctx context.Context) *dagger.File {
	return r.QuickstartBasicsEnv(ctx).
		WithExec([]string{"sh", "-c", "dagger init --sdk=go --name=basics"}).
		WithExec([]string{"vhs", "modules.tape"}).
		File("/recordings/modules.gif")
}

func (r *Recorder) QuickstartBasics6(ctx context.Context) *dagger.File {
	return r.QuickstartBasicsEnv(ctx).
		WithExec([]string{"sh", "-c", "dagger init --sdk=go --name=basics"}).
		WithExec([]string{"sh", "-c", `echo 'func (m *Basics) Env(ctx context.Context) *dagger.Container {\n  return dag.Container().From("debian:latest").WithMountedCache("/var/cache/apt/archives", dag.CacheVolume("apt-cache")).WithExec([]string{"apt-get", "update"}).WithExec([]string{"apt-get", "install", "--yes", "maven", "mariadb-server"}) \n}' >> .dagger/main.go`}).
		WithExec([]string{"vhs", "caching.tape"}).
		File("/recordings/caching.gif")
}

// QuickstartCiEnv sets up the environment for the quickstart CI recording
func (r *Recorder) QuickstartCiEnv(ctx context.Context, moduleSource *dagger.Directory) *dagger.Container {
	ctr := r.Env(ctx).
		WithDirectory("/tmp/module", moduleSource).
		WithWorkdir("/tapes/quickstart-ci").
		WithExec([]string{"sh", "-c", "git clone --depth=1 https://github.com/dagger/hello-dagger-template.git hello-dagger"}).
		WithWorkdir("/tapes/quickstart-ci/hello-dagger").
		WithExec([]string{"sh", "-c", "dagger init --sdk=go --name=hello-dagger"}).
		WithExec([]string{"sh", "-c", "cp /tmp/module/main.go .dagger/main.go"}).
		WithWorkdir("/tapes/quickstart-ci")
	return ctr
}

func (r *Recorder) QuickstartCi1(ctx context.Context, moduleSource *dagger.Directory) *dagger.File {
	return r.QuickstartCiEnv(ctx, moduleSource).
		WithExec([]string{"vhs", "publish.tape"}).
		File("/recordings/publish.gif")
}

func (r *Recorder) QuickstartCi2(ctx context.Context, moduleSource *dagger.Directory) *dagger.File {
	return r.QuickstartCiEnv(ctx, moduleSource).
		Terminal().
		WithExec([]string{"vhs", "build-service.tape"}).
		File("/recordings/build-service.gif")
}
