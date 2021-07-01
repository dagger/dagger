package testing

import "alpha.dagger.io/dagger/op"

#up: [
	op.#FetchContainer & {
		ref: "alpine"
	},
	op.#Exec & {
		args: ["sh", "-c", #"""
			echo "$foo"
			"""#]
		env: foo: lala: "lala"
	},
]
