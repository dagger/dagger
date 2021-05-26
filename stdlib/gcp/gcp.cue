package gcp

import (
	"dagger.io/dagger"
)

// Base Google Cloud Config
#Config: {
	// GCP region
	region: string @dagger(input)
	// GCP projcet
	project: string @dagger(input)
	// GCP service key
	serviceKey: dagger.#Secret @dagger(input)
}
