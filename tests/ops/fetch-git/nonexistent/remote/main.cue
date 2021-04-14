package testing

import "dagger.io/dagger/op"

#up: [
	op.#FetchGit & {
		remote: "https://github.com/blocklayerhq/lalalala.git"
		ref:    "master"
	},
]
