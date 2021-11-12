package main

import "alpha.dagger.io/dagger/op"

TestPlatformAmd: #up: [
	op.#FetchContainer & {
		ref:      "alpine"
		platform: "linux/amd64"
	},

	op.#Exec & {
		always: true
		args: ["/bin/sh", "-c", "echo $(uname -a) | grep 'x86_64'"]
	},
]

TestPlatformArm: #up: [
	op.#FetchContainer & {
		ref:      "alpine"
		platform: "linux/arm64"
	},

	op.#Exec & {
		always: true
		args: ["/bin/sh", "-c", "echo $(uname -a) | grep 'aarch64'"]
	},
]

TestPlatformS390: #up: [
	op.#FetchContainer & {
		ref:      "alpine"
		platform: "linux/s390x"
	},

	op.#Exec & {
		always: true
		args: ["/bin/sh", "-c", "echo $(uname -a) | grep 's390x'"]
	},
]
