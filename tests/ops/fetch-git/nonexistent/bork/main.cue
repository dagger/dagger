package testing

import "dagger.io/dagger/op"

#up: [
	op.#FetchGit & {
		remote: "pork://pork"
		ref:    "master"
	},
]
