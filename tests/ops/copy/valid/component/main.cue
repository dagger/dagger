package testing

import "dagger.io/dagger/op"

component: #up: [
	op.#FetchContainer & {
		ref: "alpine"
	},
	op.#Exec & {
		args: ["sh", "-c", """
			printf lol > /id
			"""]
	},
]

test1: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "busybox"
		},
		op.#Copy & {
			from: component
			src:  "/id"
			dest: "/"
		},
		op.#Export & {
			source: "/id"
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
