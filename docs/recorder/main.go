package main

import (
	"context"
	"dagger/recorder/internal/dagger"
	"fmt"
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

// Example: dagger -i call --tapes-source=tapes --docker=/var/run/docker.sock quickstart-basics-1 export --path=/tmp/out/terminal-1.webm
func (r *Recorder) QuickstartBasics1(ctx context.Context) *dagger.File {
	return r.QuickstartBasicsEnv(ctx).
		WithExec([]string{"vhs", "terminal-1.tape"}).
		File("/recordings/terminal-1.webm")
}

// Example: dagger -i call --tapes-source=tapes --docker=/var/run/docker.sock quickstart-basics-2 export --path=/tmp/out/terminal-2.webm
func (r *Recorder) QuickstartBasics2(ctx context.Context) *dagger.File {
	return r.QuickstartBasicsEnv(ctx).
		WithExec([]string{"vhs", "terminal-2.tape"}).
		File("/recordings/terminal-2.webm")
}

// Example: dagger -i call --tapes-source=tapes --docker=/var/run/docker.sock quickstart-basics-3 export --path=/tmp/out/publish-shell.webm
func (r *Recorder) QuickstartBasics3(ctx context.Context) *dagger.File {
	return r.QuickstartBasicsEnv(ctx).
		WithExec([]string{"vhs", "publish-shell.tape"}).
		File("/recordings/publish-shell.webm")
}

// Example: dagger -i call --tapes-source=tapes --docker=/var/run/docker.sock quickstart-basics-4 export --path=/tmp/out/publish-code.webm
func (r *Recorder) QuickstartBasics4(ctx context.Context) *dagger.File {
	return r.QuickstartBasicsEnv(ctx).
		WithExec([]string{"sh", "-c", "dagger init --sdk=go --name=basics"}).
		WithExec([]string{"sh", "-c", `echo 'func (m *Basics) Publish(ctx context.Context) (string, error) {\n return dag.Container().From("alpine:latest").WithNewFile("/hi.txt", "Hello from Dagger!").WithEntrypoint([]string{"cat", "/hi.txt"}).Publish(ctx, "ttl.sh/hello")  \n}' >> .dagger/main.go`}).
		WithExec([]string{"vhs", "publish-code.tape"}).
		File("/recordings/publish-code.webm")
}

// Example: dagger -i call --tapes-source=tapes --docker=/var/run/docker.sock quickstart-basics-5 export --path=/tmp/out/modules.webm
func (r *Recorder) QuickstartBasics5(ctx context.Context) *dagger.File {
	return r.QuickstartBasicsEnv(ctx).
		WithExec([]string{"sh", "-c", "dagger init --sdk=go --name=basics"}).
		WithExec([]string{"vhs", "modules.tape"}).
		File("/recordings/modules.webm")
}

// Example: dagger -i call --tapes-source=tapes --docker=/var/run/docker.sock quickstart-basics-6 export --path=/tmp/out/caching.webm
func (r *Recorder) QuickstartBasics6(ctx context.Context) *dagger.File {
	return r.QuickstartBasicsEnv(ctx).
		WithExec([]string{"sh", "-c", "dagger init --sdk=go --name=basics"}).
		WithExec([]string{"sh", "-c", `echo 'func (m *Basics) Env(ctx context.Context) *dagger.Container {\n  return dag.Container().From("debian:latest").WithMountedCache("/var/cache/apt/archives", dag.CacheVolume("apt-cache")).WithExec([]string{"apt-get", "update"}).WithExec([]string{"apt-get", "install", "--yes", "maven", "mariadb-server"}) \n}' >> .dagger/main.go`}).
		WithExec([]string{"vhs", "caching.tape"}).
		File("/recordings/caching.webm")
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

// Example: dagger -i call --docker=/var/run/docker.sock --tapes-source=tapes quickstart-ci-1 --module-source=../current_docs/quickstart/ci/snippets/go export --path=/tmp/out/publish.webm
func (r *Recorder) QuickstartCi1(ctx context.Context, moduleSource *dagger.Directory) *dagger.File {
	return r.QuickstartCiEnv(ctx, moduleSource).
		WithExec([]string{"vhs", "publish.tape"}).
		File("/recordings/publish.webm")
}

// Example: dagger -i call --docker=/var/run/docker.sock --tapes-source=tapes quickstart-ci-2 --module-source=../current_docs/quickstart/ci/snippets/go --image=ttl.sh/hello-dagger-1671552  export --path=/tmp/out/docker.webm
func (r *Recorder) QuickstartCi2(ctx context.Context, moduleSource *dagger.Directory, image string) *dagger.File {
	return r.QuickstartCiEnv(ctx, moduleSource).
		WithExec([]string{"sh", "-c", fmt.Sprintf(`sed -i 's|ttl.sh/[^\\s\"'\"']*|%s|g' docker.tape`, image)}).
		WithExec([]string{"vhs", "docker.tape"}).
		//Terminal().
		WithExec([]string{"sh", "-c", `docker container stop $(docker ps --format "{{.Image}} {{.Names}}" | grep hello-dagger | awk '{print $2}')`}).
		WithExec([]string{"sh", "-c", `docker rmi ` + image}).
		//Terminal().
		File("/recordings/docker.webm")
}

// Example: dagger -i call --docker=/var/run/docker.sock --tapes-source=tapes quickstart-ci-3 --module-source=../current_docs/quickstart/ci/snippets/go export --path=/tmp/out/buildenv-terminal.webm
func (r *Recorder) QuickstartCi3(ctx context.Context, moduleSource *dagger.Directory) *dagger.File {
	return r.QuickstartCiEnv(ctx, moduleSource).
		WithExec([]string{"vhs", "buildenv-terminal.tape"}).
		File("/recordings/buildenv-terminal.webm")
}

// Example: dagger -i call --docker=/var/run/docker.sock --tapes-source=tapes quickstart-ci-4 --module-source=../current_docs/quickstart/ci/snippets/go export --path=/tmp/out/build-service.webm
func (r *Recorder) QuickstartCi4(ctx context.Context, moduleSource *dagger.Directory) *dagger.File {
	return r.QuickstartCiEnv(ctx, moduleSource).
		WithExec([]string{"vhs", "build-service.tape"}).
		File("/recordings/build-service.webm")
}
