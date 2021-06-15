package a

import "dagger.io/dagger/op"

exp: {
	string
	#up: [
		op.#FetchContainer & {ref: "busybox"},
		op.#Exec & {
			args: ["sh", "-c", """
				printf a > /export
				"""]
		},
		op.#Export & {
			source: "/export"
			format: "string"
		},
	]
}
