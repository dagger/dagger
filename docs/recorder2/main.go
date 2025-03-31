package main

import (
	"dagger/recorder/internal/dagger"
)

type Recorder struct {
	// +private
	Vhs *dagger.Vhs
}

func New() *Recorder {
	return &Recorder{
		Vhs: dag.Vhs(dagger.VhsOpts{
			Container: dag.Container().
				// TODO: pin version
				// TODO: consider using a wolfi image instead? easier to install docker, but all fonts and dependencies need to be installed manually
				From("ghcr.io/charmbracelet/vhs:v0.9.0").

				// Install Docker
				// TODO: clean this up
				// https://docs.docker.com/engine/install/debian/#install-using-the-convenience-script
				WithEnvVariable("DEBIAN_FRONTEND", "noninteractive").
				WithExec([]string{"apt-get", "update"}).
				WithExec([]string{"apt-get", "-y", "install", "curl", "ca-certificates"}).
				WithExec([]string{"sh", "-c", "install -m 0755 -d /etc/apt/keyrings"}).
				WithExec([]string{"sh", "-c", `curl -fsSL "https://download.docker.com/linux/debian/gpg" -o /etc/apt/keyrings/docker.asc`}).
				WithExec([]string{"sh", "-c", "chmod a+r /etc/apt/keyrings/docker.asc"}).
				WithExec([]string{"sh", "-c", `echo "deb [arch=arm64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable" > /etc/apt/sources.list.d/docker.list`}).
				WithExec([]string{"apt-get", "update"}).
				WithExec([]string{"apt-get", "-y", "install", "docker-ce-cli"}).
				WithoutEnvVariable("DEBIAN_FRONTEND").

				// Install Dagger CLI
				WithFile("/usr/local/bin/dagger", dag.DaggerCli().Binary()).

				// Configure Docker
				WithEnvVariable("DOCKER_HOST", "tcp://docker:2375").
				WithServiceBinding("docker", dag.Docker().Engine(dagger.DockerEngineOpts{Persist: false})).

				// Initialize Dagger engine
				WithExec([]string{"dagger", "--command", ".help"}),
		}),
	}
}

func (r *Recorder) RenderAll() *dagger.Directory {
	return dag.Directory().
		WithDirectory("", r.RenderFeatures()).
		WithDirectory("", r.RenderQuickstart())
}

func (r *Recorder) RenderFeatures() *dagger.Directory {
	return dag.Directory().
		WithDirectory("", r.Render("features/shell-curl.tape")).
		WithDirectory("", r.Render("features/shell-build.tape")).
		WithDirectory("", r.Render("features/shell-help.tape"))
}

func (r *Recorder) RenderQuickstart() *dagger.Directory {
	return dag.Directory()
}

func (r *Recorder) Render(tape string) *dagger.Directory {
	return r.vhsInclude([]string{
		"config.tape", "shell.tape",
		tape,
	}).Render(tape)
}

func (r *Recorder) vhsInclude(include []string) *dagger.VhsWithSource {
	return r.filteredVhs(dagger.DirectoryFilterOpts{Include: include})
}

func (r *Recorder) filteredVhs(filter dagger.DirectoryFilterOpts) *dagger.VhsWithSource {
	source := dag.CurrentModule().Source().
		Directory("tapes").
		Filter(filter)

	return r.Vhs.WithSource(source)
}
