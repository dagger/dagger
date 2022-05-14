// Google Cloud Platform
package gcp

import (
	"dagger.io/dagger"
)

// Base Google Cloud Config
#Config: {
	// GCP region
	region: *null | string
	// GCP zone
	zone: *null | string
	// GCP project
	project: string
	// GCP service key
	serviceKey: dagger.#Secret
}
