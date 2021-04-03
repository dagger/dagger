package testing

import (
	"dagger.io/llb"
	"dagger.io/dagger"
)

source: dagger.#Artifact
foo:    "bar"

bar: {
	string

	#up: [
		llb.#FetchContainer & {ref: "busybox"},
		llb.#Exec & {
			args: ["cp", "/source/testfile", "/out"]
			mount: "/source": from: source
		},
		llb.#Export & {
			format: "string"
			source: "/out"
		},
	]
}
