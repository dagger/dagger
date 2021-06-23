package testing

import "alpha.dagger.io/dagger/op"

test1: {
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

test2: {
	string

	#up: [
		op.#Load & {
			from: [
				op.#FetchContainer & {
					ref: "busybox"
				},
			]
		},
		op.#Export & {
			source: "/etc/issue"
			format: "string"
		},
	]
}
