package alpine

import (
	"dagger.io/dagger"
	"dagger.io/alpine"
)

TestImageVersion: {
	image: alpine.#Image & {
		// install an old version on purpose
		version: "3.10.6"
	}

	test: #dagger: compute: [
		dagger.#Load & {from: image},
		dagger.#Exec & {
			args: [
				"sh",
				"-ec",
				"""
					test "$(cat /etc/alpine-release)" = 3.10.6
					""",
			]
		},
	]
}

TestPackageInstall: {
	image: alpine.#Image & {
		package: jq:   true
		package: curl: "=~7.74.0"
	}

	test: #dagger: compute: [
		dagger.#Load & {from: image},
		dagger.#Exec & {
			args: ["jq", "--version"]
		},
		dagger.#Exec & {
			args: ["sh", "-ec", "curl --version | grep -q 7.74.0"]
		},
	]
}
