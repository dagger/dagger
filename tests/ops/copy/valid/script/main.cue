package testing

import "alpha.dagger.io/dagger/op"

TestScriptCopy: {
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
