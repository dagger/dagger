package testing

import "alpha.dagger.io/dagger/op"

foo: "value"
bar: "another value"
computed: {
	string
	#up: [
		op.#FetchContainer & {ref: "busybox"},
		op.#Exec & {
			args: ["sh", "-c", """
				printf test > /export
				"""]
		},
		op.#Export & {
			source: "/export"
			format: "string"
		},
	]
}
