package testing

import "alpha.dagger.io/dagger/op"

TestInvalidMountPath: {
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
				from: [{
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
