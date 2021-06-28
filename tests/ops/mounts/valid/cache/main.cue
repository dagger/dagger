package testing

import (
	"alpha.dagger.io/dagger/op"
)

TestMountCache: {
	string

	#up: [
		op.#Load & {
			from: [{do: "fetch-container", ref: "alpine"}]
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo -n "$RANDOM" > /out
				"""]
			dir: "/"
			mount: something: "cache"
		},
		op.#Export & {
			source: "/out"
			format: "string"
		},
	]
}

// Make sure references to pipelines with cache mounts never get re-executed. #399
TestReference: TestMountCache
