package testing

import "alpha.dagger.io/dagger/op"

#up: [
	op.#FetchGit & {
		remote: "pork://pork"
		ref:    "master"
	},
]
