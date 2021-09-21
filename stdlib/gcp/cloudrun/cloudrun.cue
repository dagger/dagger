package cloudrun

import (
	"strings"

	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/gcp"
)

// Service deploys a Cloud Run service based on provided GCR image
#Service: {
	// GCP Config
	config: gcp.#Config

	// Cloud Run service name
	name: string & dagger.#Input

	// GCR image ref
	image: string & dagger.#Input

	// Cloud Run platform
	platform: *"managed" | string & dagger.#Input

	// Cloud Run service exposed port
	port: *"80" | string & dagger.#Input

	// Cloud Run service environment variables
	env: [string]: string
	_envVars: [ for key, val in env {key + "=" + val}]

	#up: [
		op.#Load & {
			from: gcp.#GCloud & {
				"config": config
			}
		},

		op.#Exec & {
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"-c",
				#"""
					gcloud run deploy "$SERVICE_NAME" \
						--image "$IMAGE" \
						--region "$REGION" \
						--port "$PORT" \
						--platform "$PLATFORM" \
						--allow-unauthenticated \
						--set-env-vars "$ENV_VARS"
					"""#,
			]
			env: {
				SERVICE_NAME: name
				PLATFORM:     platform
				REGION:       config.region
				IMAGE:        image
				PORT:         port
				ENV_VARS:     strings.Join(_envVars, ",")
			}
		},
	]
}
