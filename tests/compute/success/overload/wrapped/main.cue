package testing

import "alpha.dagger.io/dagger/op"

foo: {
	new_prop: "lala"
	#new_def: "lala"

	new_prop_too: string
	#new_def_too: string

	#up: [
		op.#FetchContainer & {
			ref: "busybox"
		},
		op.#Exec & {
			args: ["true"]
			dir: "/"
		},
	]
}
