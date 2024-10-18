package main

import (
	"encoding/json"

	"github.com/dagger/dagger/modules/gha/api"
	"github.com/dagger/dagger/modules/gha/internal/dagger"
	"gopkg.in/yaml.v3"
)

const genHeader = "# This file was generated. See https://daggerverse.dev/mod/github.com/dagger/dagger/modules/gha"

// Generate an overlay config directory for this workflow
func workflowConfig(
	w api.Workflow,
	// Filename of the workflow file under .github/workflows/
	filename string,
	// Encode the workflow as JSON, which is valid YAML
	asJSON bool,
) *dagger.Directory {
	var (
		contents []byte
		err      error
	)
	if asJSON {
		contents, err = json.MarshalIndent(w, "", " ")
	} else {
		contents, err = yaml.Marshal(w)
	}
	if err != nil {
		panic(err)
	}
	return dag.
		Directory().
		WithNewFile(".github/workflows/"+filename, genHeader+"\n"+string(contents))
}

// Generate a github configuration directory for this action, usable as an overlay to the repo root
// func (a Action) Config() *dagger.Directory {
// 	contents, err := json.MarshalIndent(a, "", " ")
// 	if err != nil {
// 		panic(err)
// 	}
// 	filename := path.Join(".github/actions", a.Name, "action.yml")
// 	return dag.
// 		Directory().
// 		WithNewFile(filename, string(contents))
// }

// HACK: dagger.gen.go needs this (for some reason)
type WorkflowTriggers = api.WorkflowTriggers
