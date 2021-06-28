// Google Cloud Platform
package gcp

import (
	"alpha.dagger.io/dagger"
)

// Base Google Cloud Config
#Config: {
	// GCP region
	region: string @dagger(input)
	// GCP project
	project: string @dagger(input)
	// GCP service key
	serviceKey: dagger.#Secret @dagger(input)
}
