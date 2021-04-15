package testing

import "dagger.io/dagger/op"

busybox1: #up: [
	op.#FetchContainer & {
		ref: "busybox"
	},
]

busybox2: #up: [
	op.#FetchContainer & {
		ref: "busybox:latest"
	},
]

busybox3: #up: [
	op.#FetchContainer & {
		ref: "busybox:1.33-musl"
	},
]

busybox4: #up: [
	op.#FetchContainer & {
		ref: "busybox@sha256:e2af53705b841ace3ab3a44998663d4251d33ee8a9acaf71b66df4ae01c3bbe7"
	},
]

busybox5: #up: [
	op.#FetchContainer & {
		ref: "busybox:1.33-musl@sha256:e2af53705b841ace3ab3a44998663d4251d33ee8a9acaf71b66df4ae01c3bbe7"
	},
]
