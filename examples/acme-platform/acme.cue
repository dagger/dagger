// ACME platform: everything you need to develop and ship improvements to
// the ACME clothing store.
package acme

import (
	"dagger.cloud/dagger"
	"dagger.cloud/netlify"
	"dagger.cloud/aws/ecs"
	"dagger.cloud/microstaging"
)

// Website on netlify
www: netlify & {
	domain: string | *defaultDomain

	// By default, use a generated microstaging.io domain
	// for easy environments on demand.
	let defaultDomain=microstaging.#Domain & {
		token: _
		prefix: "www.acme"
	}
}

// API deployed on ECS
api: ecs & {
	domain: _ | *defaultDomain

	let defaultDomain = microstaging.#Domain & {
		token: _
		prefix: "api.acme"
	}
}

// Database on RDS
db: rds & {
	engine: "postgresql"

}
