package testing

import "dagger.io/dagger/op"

foo: "value"
bar: "another value"

#up: [
	op.#FetchContainer & {ref: "busybox"},
	op.#Exec & {args: ["true"]},
]
