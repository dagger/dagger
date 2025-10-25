package main

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/modules/gha/api"
)

func (j *Job) checkoutStep() api.JobStep {
	return api.JobStep{
		Name: "Checkout",
		Uses: "actions/checkout@v4",
	}
}

func (j *Job) callDaggerStep() api.JobStep {
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
