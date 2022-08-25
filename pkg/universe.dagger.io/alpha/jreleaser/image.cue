package jreleaser

import (
	"universe.dagger.io/docker"
)

// JReleaser image default name
_#DefaultName: "jreleaser-slim"

// JReleleaser image default repository
_#DefaultRepository: "jreleaser"

// JReleaser image default version
_#DefaultVersion: "latest"

// JReleaser image
#Image: {
	// --== Public ==--

	name:       *_#DefaultName | string
	repository: *_#DefaultRepository | string
	version:    *_#DefaultVersion | string

	packages: [pkgName=string]: version: string | *""

	docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "\(repository)/\(name):\(version)"
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
