package testing

import "alpha.dagger.io/dagger/op"

bar: string

#up: [
	op.#FetchContainer & {
		ref: "alpine"
	},
	op.#Exec & {
		args: ["sh", "-c", """
				echo "foo: $foo"
				[ "$foo" == "overlay environment" ] || exit 1
			"""]
		env: foo: bar
	},
]
