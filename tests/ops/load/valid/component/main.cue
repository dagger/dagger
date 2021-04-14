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
		op.#Load & {
			from: component
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
		op.#Load & {
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
		},
		op.#Export & {
			source: "/id"
			format: "string"
		},
	]
}
