package testing

import "alpha.dagger.io/dagger/op"

#up: [
	op.#FetchGit & {
		remote: "https://github.com/blocklayerhq/acme-clothing.git"
		ref:    "master"
	},
]
