package main

import (
	"context"
	"errors"
	"regexp"

	"github.com/dagger/dagger/modules/gha/internal/dagger"
)

type Job struct {
	Name    string
	Command string

	// The maximum number of minutes to run the workflow before killing the process
	TimeoutMinutes int
	// Run the workflow in debug mode
	Debug bool
	// Use a sparse git checkout, only including the given paths
	// Example: ["src", "tests", "Dockerfile"]
	SparseCheckout []string
	// Enable lfs on git checkout
	LFS bool
	// Github secrets to inject into the workflow environment.
	// For each secret, an env variable with the same name is created.
	// Example: ["PROD_DEPLOY_TOKEN", "PRIVATE_SSH_KEY"]
	Secrets []string
	// Dispatch jobs to the given runner
	// Example: ["ubuntu-latest"]
	Runner []string
	// The Dagger module to load
	Module string
	// Dagger version to run this workflow
	DaggerVersion string
	// Public Dagger Cloud token, for open-source projects. DO NOT PASS YOUR PRIVATE DAGGER CLOUD TOKEN!
	// This is for a special "public" token which can safely be shared publicly.
	// To get one, contact support@dagger.io
	PublicToken string
	// Explicitly stop the dagger engine after completing the workflow.
	StopEngine bool
}

func (gha *Gha) Job(
	name string,
	command string,

	// Public Dagger Cloud token, for open-source projects. DO NOT PASS YOUR PRIVATE DAGGER CLOUD TOKEN!
	// This is for a special "public" token which can safely be shared publicly.
	// To get one, contact support@dagger.io
	// +optional
	publicToken string,
	// Explicitly stop the dagger engine after completing the workflow.
	// +optional
	stopEngine bool,
	// The maximum number of minutes to run the workflow before killing the process
	// +optional
	timeoutMinutes int,
	// Run the workflow in debug mode
	// +optional
	debug bool,
	// Use a sparse git checkout, only including the given paths
	// Example: ["src", "tests", "Dockerfile"]
	// +optional
	sparseCheckout []string,
	// Enable lfs on git checkout
	// +optional
	lfs bool,
	// Github secrets to inject into the workflow environment.
	// For each secret, an env variable with the same name is created.
	// Example: ["PROD_DEPLOY_TOKEN", "PRIVATE_SSH_KEY"]
	// +optional
	secrets []string,
	// Dispatch jobs to the given runner
	// Example: ["ubuntu-latest"]
	// +optional
	runner []string,
	// The Dagger module to load
	// +optional
	module string,
	// Dagger version to run this workflow
	// +optional
	daggerVersion string,
) *Job {
	j := &Job{
		Name:           name,
		PublicToken:    publicToken,
		StopEngine:     stopEngine,
		Command:        command,
		TimeoutMinutes: timeoutMinutes,
		Debug:          debug,
		SparseCheckout: sparseCheckout,
		LFS:            lfs,
		Secrets:        secrets,
		Runner:         runner,
		Module:         module,
		DaggerVersion:  daggerVersion,
	}
	j.applyDefaults(gha.JobDefaults)
	return j
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

func (j *Job) applyDefaults(other *Job) *Job {
	if other == nil {
		return j
	}
	setDefault(&j.PublicToken, other.PublicToken)
	setDefault(&j.StopEngine, other.StopEngine)
	setDefault(&j.TimeoutMinutes, other.TimeoutMinutes)
	setDefault(&j.Debug, other.Debug)
	mergeDefault(&j.Runner, other.Runner)
	setDefault(&j.Module, other.Module)
	setDefault(&j.DaggerVersion, other.DaggerVersion)
	return j
}
