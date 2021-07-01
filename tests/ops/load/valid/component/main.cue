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

TestComponentLoad: {
	string

	#up: [
		op.#Load & {
			from: TestComponent
		},
		op.#Export & {
			source: "/id"
			format: "string"
		},
	]
}

TestNestedLoad: {
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
