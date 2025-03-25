package main

import (
	"context"
	"dagger/recorder/internal/dagger"
	"errors"
	"time"
)

type Recorder struct {
	R     *dagger.Termcast // +private
	Error string           // +private
	//Error error            // +private
}

func New(
	// Working directory for the recording container
	// +optional
	wdir *dagger.Directory,
) Recorder {
	if wdir == nil {
		wdir = dag.Directory()
	}
	return Recorder{
		R: getTermcast(wdir),
	}
}

func getTermcast(wdir *dagger.Directory) *dagger.Termcast {
	return dag.Termcast(dagger.TermcastOpts{
		Container: dag.Wolfi().
			Container(dagger.WolfiContainerOpts{Packages: []string{"docker-cli", "curl"}}).
			WithFile("/usr/local/bin/dagger", dag.DaggerCli().Binary()).
			WithWorkdir("/src").
			WithMountedDirectory(".", wdir).
			WithEnvVariable("DOCKER_HOST", "tcp://docker:2375").
			WithServiceBinding("docker", dag.Docker().Engine(dagger.DockerEngineOpts{Persist: false})),
	})
}

func getTermcastWithEnvVar(wdir *dagger.Directory, name string, value string) *dagger.Termcast {
	ctr := getTermcast(wdir).Container().
		WithEnvVariable("CACHEBUSTER", time.Now().String()).
		WithEnvVariable(name, value)
	return dag.Termcast().WithContainer(ctr)
}

func getTermcastWithFile(wdir *dagger.Directory, path string, contents string) *dagger.Termcast {
	ctr := getTermcast(wdir).Container().WithNewFile(path, contents)
	return dag.Termcast().WithContainer(ctr)
}

func getTermcastWithQuickstart(wdir *dagger.Directory) *dagger.Termcast {
	repo := dag.Git("https://github.com/dagger/hello-dagger").
		Branch("main").Tree()

	ctr := getTermcast(dag.Directory()).Container().
		WithMountedDirectory("/src", repo).
		WithMountedDirectory("/module", wdir).
		WithExec([]string{"cp", "-R", "/module", "/src/dagger"}).
		WithExec([]string{"mv", "/src/dagger/dagger.json", "/src/dagger.json"}).
		WithExec([]string{"sh", "-c", `sed -i 's/"source": "."/"source": "dagger"/' /src/dagger.json`}).
		WithWorkdir("/src")
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

func (r Recorder) Cd(wdir string) Recorder {
	r.R = r.R.WithContainer(r.R.Container().WithWorkdir(wdir))
	return r
}

func (r Recorder) Gif(ctx context.Context) (*dagger.File, error) {
	if r.Error != "" {
		return nil, errors.New(r.Error)
	}
	return r.R.Gif(), nil
}

func (r Recorder) GenerateFeatureRecordings(
	ctx context.Context,
	// path to features/snippets dir
	base *dagger.Directory,
	githubToken *dagger.Secret,
) *dagger.Directory {
	pt, _ := githubToken.Plaintext(ctx)
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
			getTermcastWithEnvVar(base.Directory("secrets/go"), "TOKEN", pt).
				Exec("dagger call github-api --token=env://TOKEN", dagger.TermcastExecOpts{Fast: true}).
				Gif()).
		// for https://docs.dagger.io/features/secrets
		WithFile(
			"secrets-file.gif",
			getTermcastWithFile(base.Directory("secrets/go"), "/token.asc", pt).
				Exec("dagger call github-api --token=file:///token.asc", dagger.TermcastExecOpts{Fast: true}).
				Gif()).
		// for https://docs.dagger.io/features/visualization
		WithFile(
			"tui.gif",
			getTermcast(base.Directory(".")).
				Exec("dagger -m github.com/jpadams/daggerverse/trivy@v0.5.0 call scan-container --ctr=index.docker.io/alpine:latest", dagger.TermcastExecOpts{Fast: true}).
				Gif())
}

func (r Recorder) GenerateQuickstartRecordings(
	// path to quickstart/snippets dir
	base *dagger.Directory,
) *dagger.Directory {
	return dag.Directory().
		// for https://docs.dagger.io/quickstart/env
		WithFile(
			"buildenv.gif",
			getTermcastWithQuickstart(base.Directory("daggerize/go")).
				Exec("dagger -c 'build-env .'", dagger.TermcastExecOpts{Fast: true}).
				Gif()).
		// for https://docs.dagger.io/quickstart/test
		WithFile(
			"test.gif",
			getTermcastWithQuickstart(base.Directory("daggerize/go")).
				Exec("dagger -c 'test .'", dagger.TermcastExecOpts{Fast: true}).
				Gif()).
		// for https://docs.dagger.io/quickstart/build
		WithFile(
			"build.gif",
			getTermcastWithQuickstart(base.Directory("daggerize/go")).
				Exec("dagger -c 'build .'", dagger.TermcastExecOpts{Fast: true}).
				Gif()).
		/*
			// for https://docs.dagger.io/quickstart/build
			WithFile(
				"build-service.gif",
				getTermcastWithQuickstart(base.Directory("daggerize/go")).
					Exec("dagger call build --source=. as-service up --ports=8080:80", dagger.TermcastExecOpts{Fast: true}).
					Gif()).
		*/
		// for https://docs.dagger.io/quickstart/publish
		WithFile(
			"publish.gif",
			getTermcastWithQuickstart(base.Directory("daggerize/go")).
				Exec("dagger -c 'publish .'", dagger.TermcastExecOpts{Fast: true}).
				Gif()).
		// for https://docs.dagger.io/quickstart/publish
		WithFile(
			"docker.gif",
			getTermcastWithQuickstart(base.Directory("daggerize/go")).
				Exec("docker run --rm --detach --publish 8080:80 ttl.sh/hello-dagger-4362349", dagger.TermcastExecOpts{Fast: true}).
				Exec("curl localhost:8080", dagger.TermcastExecOpts{Fast: true}).
				Gif())
}
