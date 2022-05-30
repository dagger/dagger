// Google Container Registry
package gcr

import (
	"universe.dagger.io/bash"
	"universe.dagger.io/x/benjamin.reigner@epitech.eu/gcp"
)

// Credentials retriever for GCR
#Credentials: {
	// GCP Config
	config: gcp.#Config

	_gcloud: gcp.#GCloud & {
		"config": config
	}

	// GCR registry secret
	_run: bash.#Run & {
		input: _gcloud.output
		script: contents: "printf $(gcloud auth print-access-token) > /token.txt"
		export: secrets: {
			"/token.txt": _
		}
	}

	output: _run.output
}
