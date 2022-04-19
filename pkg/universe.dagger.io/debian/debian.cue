// Base package for Debian Linux
package debian

import (
	"universe.dagger.io/docker"
)

// Build a Debian Linux container image
#Build: {

	// Debian version to install.
	version: string | *"stable@sha256:7ec7bef742f919f7cc88f41b598ceeb6b74bcb446e9ce1d2d7c31eb26ccba624"

	// List of packages to install
	packages: [pkgName=string]: {
		version: string | *""
	}

	docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "index.docker.io/debian:\(version)"
			},
            docker.#Run & {
                command: {
                    name: "apt"
                    args: ["update"]
                }
            },
			for pkgName, pkg in packages {
				docker.#Run & {
					command: {
						name: "apt"
						args: ["install", "-y", "--no-install-recommends", "\(pkgName)\(pkg.version)"]
					}
				}
			},
            docker.#Run & {
                command: {
                    name: "rm"
                    args: ["-rf", "/var/lib/apt/lists/*"]
                }
            }
		]
	}
}
