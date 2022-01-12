// Base package for Alpine Linux
package alpine

import (
	"universe.dagger.io/docker"
)

// Default Alpine version
let defaultVersion = "3.13.5@sha256:69e70a79f2d41ab5d637de98c1e0b055206ba40a8145e7bddb55ccc04e13cf8f"

// Build an Alpine Linux container image
#Build: {
	// Alpine version to install
	version: string | *defaultVersion

	// List of packages to install
	packages: [pkgName=string]: version: string | *""

	docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "index.docker.io/alpine:\(version)"
			},
			for pkgName, pkg in packages {
				run: cmd: {
					name: "apk"
					args: ["add", "\(pkgName)\(version)"]
					flags: {
						"-U":         true
						"--no-cache": true
					}
				}
			},
		]
	}
}
