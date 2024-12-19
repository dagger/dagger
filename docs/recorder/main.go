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
	workdir *dagger.Directory,
) Recorder {
	if workdir == nil {
		workdir = dag.Directory()
	}
	return Recorder{
		R: dag.Termcast(dagger.TermcastOpts{
			Container: dag.Wolfi().
				Container().
				WithFile("/bin/dagger", dag.DaggerCli().Binary()).
				WithWorkdir("/src").
				WithMountedDirectory(".", workdir),
		}),
	}
}

func (r Recorder) Exec(ctx context.Context, cmd string) (Recorder, error) {
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
	r.R = r.R.Exec(cmd, dagger.TermcastExecOpts{
		Fast: true,
	}).Wait(1000)
	return r, nil
}

func (r Recorder) Debug(ctx context.Context) (Recorder, error) {
	_, err := r.R.ExecEnv().Terminal(dagger.ContainerTerminalOpts{
		ExperimentalPrivilegedNesting: true,
	}).Sync(ctx)
	return r, err
}

func (r Recorder) Cd(workdir string) Recorder {
	r.R = r.R.WithContainer(r.R.Container().WithWorkdir(workdir))
	return r
}

func (r Recorder) Gif(ctx context.Context) (*dagger.File, error) {
	if r.Error != "" {
		return nil, errors.New(r.Error)
	}
	return r.R.Gif(), nil
}
