package docker

import (
	"dagger.io/docker"
	"dagger.io/dagger/op"
	"dagger.io/alpine"
)

ref: string @dagger(input)

TestPull: {
	pull: docker.#Pull & {from: ref}

	check: #up: [
		op.#Load & {from: alpine.#Image},
		op.#Exec & {
			always: true
			args: [
				"sh", "-c", """
					 grep -q "test" /src/test.txt
					""",
			]
			mount: "/src": from: pull
		},
	]
}
