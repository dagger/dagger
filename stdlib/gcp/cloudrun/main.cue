package cloudrun

import (
	"dagger.io/dagger/op"
	"dagger.io/gcp"
)

// Deploy deploys a Cloud Run service based on provided GCR image 
#Deploy: {
	// GCP Config
	config: gcp.#Config

	// service name
	serviceName: string @dagger(input)

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
				SERVICE_NAME: serviceName
				PLATFORM:     platform
				REGION:       config.region
				IMAGE:        image
			}
		},
	]
}
