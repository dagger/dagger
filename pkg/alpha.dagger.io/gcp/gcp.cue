// Google Cloud Platform
package gcp

import (
	"alpha.dagger.io/dagger"
)

// Base Google Cloud Config
#Config: {
	// GCP region
	region: dagger.#Input & {*null | string}
	// GCP zone
	zone: dagger.#Input & {*null | string}
	// GCP project
	project: dagger.#Input & {string}
	// GCP service key
	serviceKey: dagger.#Input & {dagger.#Secret}
}
