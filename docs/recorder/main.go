package main

import (
	"context"
	"dagger/recorder/internal/dagger"
	"errors"
)

type Recorder struct {
	R     *dagger.Termcast // +private
	Error string           // +private
	//Error error            // +private
}

func New(
	// Working directory for the recording container
	// +optional
	werkdir *dagger.Directory,
) Recorder {
	if werkdir == nil {
		werkdir = dag.Directory()
	}
	return Recorder{
		R: getTermcast(werkdir),
	}
}

func getTermcast(werkdir *dagger.Directory) *dagger.Termcast {
	return dag.Termcast(dagger.TermcastOpts{
		Container: dag.Wolfi().
			Container(dagger.WolfiContainerOpts{Packages: []string{"docker-cli"}}).
			WithFile("/bin/dagger", dag.DaggerCli().Binary()).
			WithWorkdir("/src").
			WithMountedDirectory(".", werkdir).
			WithEnvVariable("DOCKER_HOST", "tcp://docker:2375").
			WithServiceBinding("docker", dag.Docker().Engine(dagger.DockerEngineOpts{Persist: false})),
	})
}

func getTermcastWithEnvVar(werkdir *dagger.Directory, name string, value string) *dagger.Termcast {
	ctr := getTermcast(werkdir).Container().WithEnvVariable(name, value)
	return dag.Termcast().WithContainer(ctr)
}

func getTermcastWithFile(werkdir *dagger.Directory, path string, contents string) *dagger.Termcast {
	ctr := getTermcast(werkdir).Container().WithNewFile(path, contents)
	return dag.Termcast().WithContainer(ctr)
}

func (r Recorder) ExecWithCacheControl(ctx context.Context, cmd string, useCache bool) (Recorder, error) {
	if useCache {
		// Dry-run to warm cache
		_, err := r.R.
			ExecEnv().
			WithExec(
				[]string{"sh", "-c", cmd},
				dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
			).
			Sync(ctx)
		if err != nil {
			return r, err
		}
	}
	r.R = r.R.Exec(cmd, dagger.TermcastExecOpts{
		Fast: true,
	}).Wait(1000)
	return r, nil
}

func (r Recorder) Exec(ctx context.Context, cmd string) (Recorder, error) {
	return r.ExecWithCacheControl(ctx, cmd, true)
}

func (r Recorder) ExecNoCache(ctx context.Context, cmd string) (Recorder, error) {
	return r.ExecWithCacheControl(ctx, cmd, false)
}

func (r Recorder) ExecInCtr(ctx context.Context, cmd string) (Recorder, error) {
	_, err := r.R.Container().WithExec([]string{"sh", "-c", cmd}).Sync(ctx)
	if err != nil {
		return r, err
	}
	return r, nil
}

func (r Recorder) Debug(ctx context.Context) (Recorder, error) {
	_, err := r.R.ExecEnv().Terminal(dagger.ContainerTerminalOpts{
		ExperimentalPrivilegedNesting: true,
	}).Sync(ctx)
	return r, err
}

func (r Recorder) Cd(werkdir string) Recorder {
	r.R = r.R.WithContainer(r.R.Container().WithWorkdir(werkdir))
	return r
}

func (r Recorder) Gif(ctx context.Context) (*dagger.File, error) {
	if r.Error != "" {
		return nil, errors.New(r.Error)
	}
	return r.R.Gif(), nil
}

func (r Recorder) GenerateFeatureRecordings(
	// path to features/snippets dir
	base *dagger.Directory,
	githubToken string,
) *dagger.Directory {
	return dag.Directory().
		// for https://docs.dagger.io/features/programmable-pipelines
		WithFile(
			"build.gif",
			getTermcast(base.Directory("programmable-pipelines-1/go")).
				Exec("dagger call build --src=https://github.com/golang/example#master:/hello --arch=amd64 --os=linux", dagger.TermcastExecOpts{Fast: true}).
				Gif()).
		// for https://docs.dagger.io/features/programmable-pipelines
		WithFile(
			"build-publish.gif",
			getTermcast(base.Directory("programmable-pipelines-1/go")).
				Exec("dagger call build --src=https://github.com/golang/example#master:/hello --arch=amd64 --os=linux publish --address=ttl.sh/my-img", dagger.TermcastExecOpts{Fast: true}).
				Gif()).
		// for https://docs.dagger.io/features/programmable-pipelines
		WithFile(
			"build-export.gif",
			getTermcast(base.Directory("programmable-pipelines-2/go")).
				Exec("dagger call build --src=https://github.com/golang/example#master:/hello --arch=amd64 --os=linux export --path=/tmp/out", dagger.TermcastExecOpts{Fast: true}).
				Gif()).
		// https://docs.dagger.io/features/secrets
		WithFile(
			"secrets-env.gif",
			getTermcastWithEnvVar(base.Directory("secrets/go"), "TOKEN", githubToken).
				Exec("dagger call github-api --token=env://TOKEN", dagger.TermcastExecOpts{Fast: true}).
				Gif()).
		// for https://docs.dagger.io/features/secrets
		WithFile(
			"secrets-file.gif",
			getTermcastWithFile(base.Directory("secrets/go"), "/token.asc", githubToken).
				Exec("dagger call github-api --token=file:///token.asc", dagger.TermcastExecOpts{Fast: true}).
				Gif()).
		// for https://docs.dagger.io/features/visualization
		WithFile(
			"tui.gif",
			getTermcast(base.Directory(".")).
				Exec("dagger -m github.com/jpadams/daggerverse/trivy@v0.5.0 call scan-container --ctr=index.docker.io/alpine:latest", dagger.TermcastExecOpts{Fast: true}).
				Gif())
}
