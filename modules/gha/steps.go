package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/modules/gha/api"
)

func (j *Job) warmEngineStep() api.JobStep {
	return j.bashStep("warm-engine", nil)
}

func (j *Job) checkoutStep() api.JobStep {
	return api.JobStep{
		Name: "Checkout",
		Uses: "actions/checkout@v4",
	}
}

func (j *Job) installDaggerSteps() []api.JobStep {
	steps := []api.JobStep{
		{
			Name: "Install Dagger",
			Uses: "dagger/dagger-for-github@" + DaggerForGithubVersion,
			With: map[string]string{
				"version": j.DaggerVersion,
			},
		},
	}

	if j.DaggerDev != "" {
		// create separate outputs and containers for each job run (to prevent
		// collisions with shared docker containers).
		engineCtr := "dagger-engine.dev-${{ github.run_id }}-${{ github.job }}"
		engineImage := "localhost/dagger-engine.dev:${{ github.run_id }}-${{ github.job }}"

		steps = append(steps, j.bashStep("start-dev-dagger", map[string]string{
			"DAGGER_REF":                         j.DaggerDev,
			"_EXPERIMENTAL_DAGGER_DEV_CONTAINER": engineCtr,
			"_EXPERIMENTAL_DAGGER_DEV_IMAGE":     engineImage,
		}))
	}

	return steps
}

func (j *Job) callDaggerStep() api.JobStep {
	// for dagger dev engines continue to use the exec step so that we don't
	// override the dagger CLI
	if j.DaggerDev != "" {
		return j.callDaggerWithExecStep()
	}

	env := map[string]string{}
	// Inject user-defined secrets
	for _, secretName := range j.Secrets {
		env[secretName] = fmt.Sprintf("${{ secrets.%s }}", secretName)
	}

	with := map[string]string{
		"call": j.Command,
		// TODO: once we release it we can add it
		// "enable-github-summary": "true",
	}

	if j.CloudEngine {
		with["dagger-flags"] = "--cloud=true"
	}

	// Inject module name
	if j.Module != "" {
		with["module"] = j.Module
	}

	// Inject Public Dagger Cloud token
	if j.PublicToken != "" {
		with["cloud-token"] = j.PublicToken
	}

	if j.DaggerVersion != "" {
		with["version"] = j.DaggerVersion
	}

	return api.JobStep{
		ID:   "exec",
		Name: "exec",
		Uses: "dagger/dagger-for-github@" + DaggerForGithubVersion,
		With: with,
		Env:  env,
	}
}

func (j *Job) callDaggerWithExecStep() api.JobStep {
	env := map[string]string{}
	// Debug mode
	if j.Debug {
		env["DEBUG"] = "1"
	}
	// Inject dagger command
	env["COMMAND"] = "dagger call " + j.Command
	// Inject user-defined secrets
	for _, secretName := range j.Secrets {
		env[secretName] = fmt.Sprintf("${{ secrets.%s }}", secretName)
	}

	// Pass along .env lines
	if len(j.Env) > 0 {
		env["DOTENV"] = strings.Join(j.Env, "\n")
	}

	// Inject module name
	if j.Module != "" {
		env["DAGGER_MODULE"] = j.Module
	}

	// Inject Public Dagger Cloud token
	if j.PublicToken != "" {
		env["DAGGER_CLOUD_TOKEN"] = j.PublicToken
	}

	return j.bashStep("exec", env)
}

func (j *Job) stopEngineStep() api.JobStep {
	return j.bashStep("scripts/stop-engine.sh", nil)
}

// Return a github actions step which executes the script embedded at scripts/<filename>.sh
// The script must be checked in with the module source code.
func (j *Job) bashStep(id string, env map[string]string) api.JobStep {
	filename := "scripts/" + id + ".sh"
	script, err := dag.
		CurrentModule().
		Source().
		File(filename).
		Contents(context.Background())
	if err != nil {
		// We skip error checking for simplicity
		// (don't want to plumb error checking everywhere)
		panic(err)
	}
	return api.JobStep{
		Name:  filename,
		ID:    id,
		Shell: "bash",
		Run:   script,
		Env:   env,
	}
}
