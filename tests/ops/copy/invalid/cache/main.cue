package testing

import "dagger.io/dagger/op"

TestCacheCopyLoadAlpine: {
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

TestCacheCopy: {
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
