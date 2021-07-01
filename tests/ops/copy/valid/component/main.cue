package testing

import "alpha.dagger.io/dagger/op"

TestComponent: #up: [
	op.#FetchContainer & {
		ref: "alpine"
	},
	op.#Exec & {
		args: ["sh", "-c", """
			printf lol > /id
			"""]
	},
]

TestComponentCopy: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "busybox"
		},
		op.#Copy & {
			from: TestComponent
			src:  "/id"
			dest: "/"
		},
		op.#Export & {
			source: "/id"
			format: "string"
		},
	]
}

TestNestedCopy: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "busybox"
		},
		op.#Copy & {
			from: #up: [
				op.#FetchContainer & {
					ref: "alpine"
				},
				op.#Exec & {
					args: ["sh", "-c", """
						printf lol > /id
						"""]
				},
			]
			src:  "/id"
			dest: "/"
		},
		op.#Export & {
			source: "/id"
			format: "string"
		},
	]
}
