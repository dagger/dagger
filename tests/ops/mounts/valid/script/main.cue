package testing

import "dagger.io/dagger/op"

test: {
	string

	#up: [
		op.#Load & {
			from: [
				op.#FetchContainer & {
					ref: "alpine"
				},
			]
		},
		op.#Exec & {
			args: ["sh", "-c", """
					ls -lA /lol > /out
				"""]
			mount: something: {
				input: [{
					do:  "fetch-container"
					ref: "alpine"
				}]
				path: "/lol"
			}
		},
		op.#Export & {
			source: "/out"
			format: "string"
		},
	]
}
