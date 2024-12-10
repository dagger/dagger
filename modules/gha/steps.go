package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/modules/gha/api"
	"golang.org/x/mod/semver"
)

func (j *Job) checkoutStep() api.JobStep {
	step := api.JobStep{
		Name: "Checkout",
		Uses: "actions/checkout@v4",
		With: map[string]string{},
	}
	if j.SparseCheckout != nil {
		// Include common dagger paths in the checkout, to make
		// sure local modules work by default
		// FIXME: this is only a guess, we need the 'source' field of dagger.json
		//  to be sure.
		sparseCheckout := append([]string{}, j.SparseCheckout...)
		sparseCheckout = append(sparseCheckout, "dagger.json", ".dagger", "dagger", "ci")
		step.With["sparse-checkout"] = strings.Join(sparseCheckout, "\n")
	}
	if j.LFS {
		step.With["lfs"] = "true"
	}
	return step
}

func (j *Job) warmEngineStep() api.JobStep {
	return j.bashStep("warm-engine", nil)
}

func (j *Job) installDaggerSteps() []api.JobStep {
	if v := j.DaggerVersion; (v == "latest") || (semver.IsValid(v)) {
		return []api.JobStep{
			j.bashStep("install-dagger", map[string]string{"DAGGER_VERSION": v}),
		}
	}
	// Interpret dagger version as a local source, and build it (dev engine)
	return []api.JobStep{
		// Install latest dagger to bootstrap dev dagger
		// FIXME: let's daggerize this, using dagger in dagger :)
		j.bashStep("install-dagger", map[string]string{"DAGGER_VERSION": "latest"}),
		{
			Name: "Install go",
			Uses: "actions/setup-go@v5",
			With: map[string]string{
				"go-version":            "1.23",
				"cache-dependency-path": ".dagger/go.sum",
			},
		},
		j.bashStep("start-dev-dagger", map[string]string{
			"DAGGER_SOURCE": j.DaggerVersion,
			// create separate outputs and containers for each job run (to prevent
			// collisions with shared docker containers).
			"_EXPERIMENTAL_DAGGER_DEV_OUTPUT":    "./bin/dev-${{ github.run_id }}-${{ github.job }}",
			"_EXPERIMENTAL_DAGGER_DEV_CONTAINER": "dagger-engine.dev-${{ github.run_id }}-${{ github.job }}",
		}),
	}
}

func (j *Job) callDaggerStep() api.JobStep {
	env := map[string]string{}
	// Debug mode
	if j.Debug {
		env["DEBUG"] = "1"
	}
	// Inject dagger command
	env["COMMAND"] = "dagger call -q " + j.Command
	// Inject user-defined secrets
	for _, secretName := range j.Secrets {
		env[secretName] = fmt.Sprintf("${{ secrets.%s }}", secretName)
	}
	// Inject module name
	if j.Module != "" {
		env["DAGGER_MODULE"] = j.Module
	}

	// Inject Public Dagger Cloud token
	if j.PublicToken != "" {
		env["DAGGER_CLOUD_TOKEN"] = j.PublicToken
		// For backwards compatibility with older engines
		env["_EXPERIMENTAL_DAGGER_CLOUD_TOKEN"] = j.PublicToken
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
