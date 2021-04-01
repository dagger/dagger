package testing

import "dagger.io/llb"

foo: "value"
bar: "another value"

#compute: [
	llb.#FetchContainer & {ref: "busybox"},
	llb.#Exec & {args: ["true"]},
]
