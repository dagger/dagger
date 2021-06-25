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
		echo \(hello)
		echo "This test SHOULD fail, because this script SHOULD execute, since bar is not referenced"
		exit 1
		"""]
	},
]
