package cloudrun

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/gcp"
)

// Service deploys a Cloud Run service based on provided GCR image 
#Service: {
	// GCP Config
	config: gcp.#Config

	// service name
	name: string @dagger(input)

	// GCR image ref
	image: string @dagger(input)

	// Cloud Run platform
	platform: *"managed" | string @dagger(input)

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
					gcloud run deploy "$SERVICE_NAME" --image "$IMAGE" --region "$REGION" --platform "$PLATFORM" --allow-unauthenticated
					"""#,
			]
			env: {
				SERVICE_NAME: name
				PLATFORM:     platform
				REGION:       config.region
				IMAGE:        image
			}
		},
	]
}
