package testing

import "alpha.dagger.io/dagger/op"

A: {
	result: string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
				echo '{"result": "from A"}' > /tmp/out
				""",
			]
		},
		op.#Export & {
			// Source path in the container
			source: "/tmp/out"
			format: "json"
		},
	]
}

B: {
	result: string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			env: DATA: A.result
			args: ["sh", "-c", """
				echo "{\\"result\\": \\"dependency $DATA\\"}" > /tmp/out
				""",
			]
			dir: "/"
		},
		op.#Export & {
			// Source path in the container
			source: "/tmp/out"
			format: "json"
		},
	]
}
