package alpine

import (
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger/op"
)

TestImageVersion: {
	image: alpine.#Image & {
		// install an old version on purpose
		version: "3.10.9"
	}

	test: #up: [
		op.#Load & {from: image},
		op.#Exec & {
			args: [
				"sh",
				"-ec",
				"""
					test "$(cat /etc/alpine-release)" = 3.10.9
					""",
			]
		},
	]
}

TestPackageInstall: {
	image: alpine.#Image & {
		package: jq:   true
		package: curl: true
		version: "3.13"
	}

	test: #up: [
		op.#Load & {from: image},
		op.#Exec & {
			args: ["jq", "--version"]
		},
		op.#Exec & {
			args: ["sh", "-ec", "curl --version"]
		},
	]
}
