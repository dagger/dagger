package git

import (
	"dagger.io/dagger"
)

// A git repository
#Repository: {

	remote: string
	ref:    string

	#compute: [
		dagger.#FetchGit & {
			"remote": remote
			"ref":    ref
		},
	]
}
