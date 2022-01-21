package docker

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

#TestStep: {
	input:  docker.#Image
	output: docker.#Image

	echo: string

	// FIXME: changing to `run` fixes the issue:
	// actions.build._dag."1"._run: 1 errors in empty disjunction:
	// actions.build._dag."1"._run: structural cycle
	// actions.build._dag."1".output: 1 errors in empty disjunction::
	//    ./custom-build-step-test.cue:24:10 
	_run: docker.#Run & {
		"input": input
		cmd: {
			name: "echo"
			args: [echo]
		}
	}

	output: _run.output
}

dagger.#Plan & {
	actions: build: docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "alpine"
			},
			#TestStep & {
				echo: "foo"
			},
			#TestStep & {
				echo: "bar"
			},
		]
	}
}
