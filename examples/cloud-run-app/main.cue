package main

import (
	"dagger.io/gcp"
	"dagger.io/gcp/cloudrun"
)

// Cloud Run service name
serviceName: *"cloudrun-test" | string @dagger(input)

// GCP region
region: *"us-west2" | string @dagger(input)

// Image name
image: *"gcr.io/dagger-ci/cloudrun-test" | string @dagger(input)

gcpConfig: gcp.#Config & {
	region: region
}

deploy: cloudrun.#Deploy & {
	serviceName: serviceName
	image:       image
	config:      gcpConfig
	region:      region
}
