package testing

import "dagger.io/dagger/op"

test: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "busybox"
		},
		op.#Copy & {
			from: [
				op.#FetchContainer & {
					ref: "alpine"
				},
			]
			src:  "/etc/issue"
			dest: "/"
		},
		op.#Export & {
			source: "/issue"
			format: "string"
		},
	]
}
