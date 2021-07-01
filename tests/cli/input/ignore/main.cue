package testing

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/dagger"
)

source: dagger.#Artifact

#up: [
	op.#FetchContainer & {ref: "busybox"},
	op.#Exec & {
		args: ["sh", "-c", """
			set -exu
			[ -f /source/testfile ]
			[ ! -d /source/.dagger ]
			"""]
		mount: "/source": from: source
	},
]
