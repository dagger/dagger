package git

import (
	"dagger.io/dagger/op"
)

// A git repository
#Repository: {

	remote: string
	ref:    string
	subdir: string | *""

	#up: [
		op.#FetchGit & {
			"remote": remote
			"ref":    ref
		},
		if subdir != "" {
			op.#Subdir & {
				dir: subdir
			}
		},
	]
}
