package testing

import "dagger.io/dagger/op"

#up: [
	op.#FetchContainer & {
		ref: "alpine"
	},
	op.#Exec & {
		args: ["sh", "-c", """
				echo "pwd is: $(pwd)"
				[ "$(pwd)" == "/thisisnonexistent" ] || exit 1
			"""]
		dir: "/thisisnonexistent"
	},
]
