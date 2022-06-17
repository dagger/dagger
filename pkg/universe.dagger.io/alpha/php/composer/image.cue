package composer

import (
	"universe.dagger.io/docker"
)

// Go image default version
_#DefaultVersion: "latest"

// Build a go base image
#Image: {
	version: *_#DefaultVersion | string

	packages: [pkgName=string]: version: string | *""

	packages: git: _

	docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "index.docker.io/composer:\(version)"
			},
			for pkgName, pkg in packages {
				docker.#Run & {
					command: {
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
