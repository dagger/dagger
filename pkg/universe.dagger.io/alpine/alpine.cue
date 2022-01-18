// Base package for Alpine Linux
package alpine

import (
	"universe.dagger.io/docker"
)

// Build an Alpine Linux container image
#Build: {
	// Alpine version to install
	version: string | *"latest"

	// List of packages to install
	packages: [pkgName=string]: version: string | *""

	docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "index.docker.io/alpine:\(version)"
			},
			for pkgName, pkg in packages {
				docker.#Run & {
					cmd: {
						name: "apk"
						args: ["add", "\(pkgName)\(pkg.version)"]
						flags: {
							"-U":         true
							"--no-cache": true
						}
					}
				}
			},
		]
	}
}
