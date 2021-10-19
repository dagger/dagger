package trivy

import (
	"alpha.dagger.io/dagger"
)

// Set Trivy download source
// - AWS
// - GCP
// - Docker Hub
// - Self Hosted

// Trivy configuration
#Config: {
	// Download source (AWS, GCP, Docker Hub, Self hosted)
	source: string

	// Trivy Image arguments
	args: [arg=string]: string

	username: dagger.#Input & {*null | dagger.#Secret}
	password: dagger.#Input & {*null | dagger.#Secret}
	ssl:      *true | bool
}
