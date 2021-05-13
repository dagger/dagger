package testing

import "dagger.io/dagger/op"

repo1: #up: [
	op.#FetchGit & {
		remote: "https://github.com/blocklayerhq/acme-clothing.git"
		ref:    "master"
	},
]

repo2: #up: [
	op.#FetchGit & {
		remote:     "https://github.com/blocklayerhq/acme-clothing.git"
		ref:        "master"
		keepGitDir: true
	},
]

#up: [
	op.#FetchContainer & {
		ref: "alpine"
	},
	op.#Exec & {
		args: ["sh", "-c", """
			set -eu
			[ ! -d /repo1/.git ]
			[ -d /repo2/.git ]
			"""]
		mount: {
			"/repo1": from: repo1
			"/repo2": from: repo2
		}
	},
]
