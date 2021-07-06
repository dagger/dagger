package docker

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

ref: string @dagger(input)

TestPull: {
	container: #RemoteContainer & {from: ref}

	test: #up: [
		op.#Load & {from: alpine.#Image},
		op.#Exec & {
			always: true
			args: [
				"sh", "-c", """
					 grep -q "test" /src/test.txt
					""",
			]
			mount: "/src": from: container
		},
	]
}
