// Base package for Alpine Linux
package alpine

import (
	"universe.dagger.io/docker"
)

_arches: {
	"linux/amd64":    "3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
	"linux/arm64/v8": "3.15.0@sha256:c74f1b1166784193ea6c8f9440263b9be6cae07dfe35e32a5df7a31358ac2060"
}

// Build an Alpine Linux container image
#Build: {
	// Architecture to support
	arch: string | *"linux/amd64"

	// Alpine version to install
	version: string | *_arches[arch]

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
