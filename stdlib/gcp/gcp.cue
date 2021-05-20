package gcp

import (
	"dagger.io/dagger"
)

// Base Google Cloud Config
#Config: {
	// GCP region
	region: string
	// GCP projcet
	project: string
	// GCP service key
	serviceKey: dagger.#Secret
}
