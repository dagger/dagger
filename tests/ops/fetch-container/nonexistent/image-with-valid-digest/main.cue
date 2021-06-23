package testing

import "alpha.dagger.io/dagger/op"

// XXX WATCHOUT
// Once buildkit has pulled that digest, it will stay cached and happily succeed WHATEVER the image name then is
#up: [
	op.#FetchContainer & {
		ref: "busyboxaaa@sha256:e2af53705b841ace3ab3a44998663d4251d33ee8a9acaf71b66df4ae01c3bbe7"
	},
]
