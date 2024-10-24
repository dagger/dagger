package main

import (
	"context"
	"errors"
	"regexp"

	"github.com/dagger/dagger/modules/gha/internal/dagger"
)

// Check that the workflow is valid, in a best effort way
func (w *Workflow) Check(
	ctx context.Context,
	// +defaultPath="/"
	repo *dagger.Directory,
) error {
	for _, job := range w.Jobs {
		if err := job.checkSecretNames(); err != nil {
			return err
		}
		if err := job.checkCommandAndModule(ctx, repo); err != nil {
			return err
		}
	}
	return nil
}

func (j *Job) checkSecretNames() error {
	// check if the secret name contains only alphanumeric characters and underscores.
	validName := regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	for _, secretName := range j.Secrets {
		if !validName.MatchString(secretName) {
			return errors.New("invalid secret name: '" + secretName + "' must contain only alphanumeric characters and underscores")
		}
	}
	return nil
}

func (j *Job) checkCommandAndModule(ctx context.Context, repo *dagger.Directory) error {
	script := "dagger call"
	if j.Module != "" {
		script = script + " -m '" + j.Module + "' "
	}
	script = script + j.Command + " --help"
	_, err := dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{
			Packages: []string{"dagger", "bash"},
		}).
		WithMountedDirectory("/src", repo).
		WithWorkdir("/src").
		WithExec(
			[]string{"bash", "-c", script},
			dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
		).
		Sync(ctx)
	return err
}
