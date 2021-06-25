package testing

import "alpha.dagger.io/dagger/op"

#up: [
	op.#FetchContainer & {
		ref: "alpine"
	},
	op.#Exec & {
		args: ["sh", "-c", """
				echo "pwd is: $(pwd)"
				[ "$(pwd)" == "/etc" ] || exit 1
			"""]
		dir: "/etc"
	},
]
