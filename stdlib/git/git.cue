package git

import (
	"dagger.io/dagger/op"
)

// A git repository
#Repository: {

	remote: string @dagger(input)
	ref:    string @dagger(input)
	subdir: string | *"" @dagger(input)

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
