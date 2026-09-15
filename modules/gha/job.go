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

	// Make the job conditional on an expression
	Condition string

	// Additional commands to run before the main one
	SetupCommands []string
	// Additional commands to run after the main one
	TeardownCommands []string

	// The maximum number of minutes to run the workflow before killing the process
	TimeoutMinutes int
	// Run the workflow in debug mode
	Debug bool
	// Github secrets to inject into the workflow environment.
	// For each secret, an env variable with the same name is created.
	// Example: ["PROD_DEPLOY_TOKEN", "PRIVATE_SSH_KEY"]
	Secrets []string
	// Lines to append to .env.
	// Example: ["OPENAI_API_KEY=op://CI/openai-api-key"]
	Env []string
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
	// CloudEngine indicates whether to use Dagger Cloud Engine to run this workflow.
	CloudEngine bool
	// Explicitly stop the dagger engine after completing the workflow.
	StopEngine bool
}

func (gha *Gha) Job(
	name string,
	command string,

	// Only run the job if this condition expression succeeds.
	// +optional
	condition string,

	// Additional commands to run before the main one.
	// +optional
	setupCommands []string,
	// Additional commands to run after the main one.
	// +optional
	teardownCommands []string,

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
	// Github secrets to inject into the workflow environment.
	// For each secret, an env variable with the same name is created.
	// Example: ["PROD_DEPLOY_TOKEN", "PRIVATE_SSH_KEY"]
	// +optional
	secrets []string,
	// Dagger secret URIs to load and assign as env variables.
	// Example: ["OPENAI_API_KEY=op://CI/openai-api-key"]
	// +optional
	env []string,
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
	// cloudEngine indicates whether to use Dagger Cloud Engine to run this workflow.
	// +optional
	cloudEngine bool,
) *Job {
	j := &Job{
		Condition:        condition,
		Name:             name,
		PublicToken:      publicToken,
		StopEngine:       stopEngine,
		Command:          command,
		SetupCommands:    setupCommands,
		TeardownCommands: teardownCommands,
		TimeoutMinutes:   timeoutMinutes,
		Debug:            debug,
		Secrets:          secrets,
		Env:              env,
		Runner:           runner,
		Module:           module,
		DaggerVersion:    daggerVersion,
		CloudEngine:      cloudEngine,
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
	mergeDefault(&j.SetupCommands, other.SetupCommands)
	mergeDefault(&j.TeardownCommands, other.TeardownCommands)
	setDefault(&j.CloudEngine, other.CloudEngine)
	return j
}
