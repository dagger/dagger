package testing

import "dagger.io/dagger/op"

test1: {
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

test2: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "busybox"
		},
		op.#Copy & {
			from: [
				op.#FetchContainer & {
					ref: "busybox"
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
