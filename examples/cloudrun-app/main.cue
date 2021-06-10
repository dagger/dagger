package main

import (
	"dagger.io/gcp"
	"dagger.io/gcp/cloudrun"
)

// Cloud Run service name
serviceName: *"cloudrun-test" | string @dagger(input)

// Image name
image: string @dagger(input)

gcpConfig: gcp.#Config

deploy: cloudrun.#Deploy & {
	"serviceName": serviceName
	"image":       image
	config:        gcpConfig
}
