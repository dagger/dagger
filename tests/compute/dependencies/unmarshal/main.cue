package testing

import (
	"encoding/json"
	"alpha.dagger.io/dagger/op"
)

A: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
				echo '{"hello": "world"}' > /tmp/out
				""",
			]
			dir: "/"
		},
		op.#Export & {
			// Source path in the container
			source: "/tmp/out"
			format: "string"
		},
	]
}

unmarshalled: json.Unmarshal(A)

B: {
	result: string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
				echo "{\\"result\\": \\"unmarshalled.hello=\(unmarshalled.hello)\\"}" > /tmp/out
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
