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
			dir: "/"
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
			args: ["sh", "-c", """
				echo "{\\"result\\": \\"dependency \(A.result)\\"}" > /tmp/out
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
