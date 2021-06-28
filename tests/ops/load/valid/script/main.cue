package testing

import "alpha.dagger.io/dagger/op"

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
		op.#Export & {
			source: "/etc/issue"
			format: "string"
		},
	]
}
