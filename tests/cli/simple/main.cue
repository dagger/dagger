package testing

import "dagger.io/llb"

foo: "value"
bar: "another value"

#up: [
	llb.#FetchContainer & {ref: "busybox"},
	llb.#Exec & {args: ["true"]},
]
