package git

import (
	"dagger.io/llb"
)

// A git repository
#Repository: {

	remote: string
	ref:    string

	#compute: [
		llb.#FetchGit & {
			"remote": remote
			"ref":    ref
		},
	]
}
