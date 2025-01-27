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
		R: dag.Termcast(dagger.TermcastOpts{
			Container: dag.Wolfi().
				Container(dagger.WolfiContainerOpts{Packages: []string{"docker-cli"}}).
				WithFile("/bin/dagger", dag.DaggerCli().Binary()).
				WithWorkdir("/src").
				WithMountedDirectory(".", werkdir).
				WithEnvVariable("DOCKER_HOST", "tcp://docker:2375").
				WithServiceBinding("docker", dag.Docker().Engine(dagger.DockerEngineOpts{Persist: false})),
		}),
	}
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
