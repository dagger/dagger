package b

import "alpha.dagger.io/dagger/op"

exp: {
	string
	#up: [
		op.#FetchContainer & {ref: "busybox"},
		op.#Exec & {
			args: ["sh", "-c", """
				printf b > /export
				"""]
		},
		op.#Export & {
			source: "/export"
			format: "string"
		},
	]
}
