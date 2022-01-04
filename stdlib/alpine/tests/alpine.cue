package alpine

import (
	"alpha.dagger.io/dagger/op"
)

TestImageVersion: {
	image: #Image & {
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
	image: #Image & {
		package: jq:   true
		package: curl: true
		version: "3.15.0"
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
