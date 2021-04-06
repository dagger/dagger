package git

import (
	"dagger.io/dagger/op"
)

// A git repository
#Repository: {

	remote: string
	ref:    string

	#up: [
		op.#FetchGit & {
			"remote": remote
			"ref":    ref
		},
	]
}
