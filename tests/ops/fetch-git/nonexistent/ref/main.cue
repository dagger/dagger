package testing

import "dagger.io/dagger/op"

#up: [
	op.#FetchGit & {
		remote: "https://github.com/blocklayerhq/acme-clothing.git"
		ref:    "lalalalal"
	},
]
