package alpine

import (
	"universe.dagger.io/docker"
)

TestImageVersion: {
	build: #Build & {
		// install an old version on purpose
		version: "3.10.9"
	}

	check: docker.#Run & {
		image: build.output
		output: files: "/etc/alpine-release": contents: "3.10.9"
	}
}

TestPackageInstall: {
	build: #Build & {
		packages: {
			jq: {}
			curl: {}
		}
	}

	check: docker.#Run & {
		script: """
			jq --version > /jq-version.txt
			curl --version > /curl-version.txt
			"""

		output: files: {
			"/jq-version.txt": contents:   "FIXME"
			"/curl-version.txt": contents: "FIXME"
		}
	}
}
