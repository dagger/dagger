package testing

import "alpha.dagger.io/dagger/op"

hello: "world"
bar:   string

#up: [
	op.#FetchContainer & {
		ref: "alpine"
	},
	op.#Exec & {
		dir: "/"
		args: ["sh", "-c", """
		echo \(bar)
		echo "This test SHOULD succeed, because this is never going to be executed, as \(bar) is not concrete"
		exit 1
		"""]
	},
]
