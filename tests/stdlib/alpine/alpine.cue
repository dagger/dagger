package alpine

import (
	"dagger.io/alpine"
	"dagger.io/llb"
)

TestImageVersion: {
	image: alpine.#Image & {
		// install an old version on purpose
		version: "3.10.6"
	}

	test: #up: [
		llb.#Load & {from: image},
		llb.#Exec & {
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

	test: #up: [
		llb.#Load & {from: image},
		llb.#Exec & {
			args: ["jq", "--version"]
		},
		llb.#Exec & {
			args: ["sh", "-ec", "curl --version | grep -q 7.74.0"]
		},
	]
}
