package docker

import (
	"dagger.io/dagger/op"
	"dagger.io/docker"
)

TestImageFromRegistry: {
	image: docker.#Pull & {
		from: "index.docker.io/the0only0vasek/alpine-test"
	}

	verify: #up: [
		op.#Load & {
			from: image
		},

		op.#Exec & {
			always: true
			args: [
				"sh", "-c", """
						grep -q "test" /test.txt
					""",
			]
		},
	]
}
