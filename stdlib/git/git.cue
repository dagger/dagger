// Git operations
package git

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

// A git repository
#Repository: {
	// Git remote.
	// Example: `"https://github.com/dagger/dagger"`
	remote: string @dagger(input)

	// Git ref: can be a commit, tag or branch.
	// Example: "main"
	ref: string @dagger(input)

	// (optional) Subdirectory
	subdir: string | *null @dagger(input)

	#up: [
		op.#FetchGit & {
			"remote": remote
			"ref":    ref
		},
		if subdir != null {
			op.#Subdir & {
				dir: subdir
			}
		},
	]
}

// Get the name of the current checked out branch or tag
#CurrentBranch: {
	// Git repository
	repository: dagger.#Artifact @dagger(input)

	// Git branch name
	name: {
		string

		#up: [
			op.#Load & {
				from: alpine.#Image & {
					package: bash: "=~5.1"
					package: git:  "=~2.30"
				}
			},

			op.#Exec & {
				mount: "/repository": from: repository
				dir: "/repository"
				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					"-c",
					#"""
						printf "$(git symbolic-ref -q --short HEAD || git describe --tags --exact-match)" > /name.txt
						"""#,
				]
			},

			op.#Export & {
				source: "/name.txt"
				format: "string"
			},
		]
	} @dagger(output)
}

// List tags of a repository
#Tags: {
	// Git repository
	repository: dagger.#Artifact @dagger(input)

	// Repository tags
	tags: {
		[...string]

		#up: [
			op.#Load & {
				from: alpine.#Image & {
					package: bash: "=~5.1"
					package: jq:   "=~1.6"
					package: git:  "=~2.30"
				}
			},

			op.#Exec & {
				mount: "/repository": from: repository
				dir: "/repository"
				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					"-c",
					#"""
						git tag -l | jq --raw-input --slurp 'split("\n") | map(select(. != ""))' > /tags.json
						"""#,
				]
			},

			op.#Export & {
				source: "/tags.json"
				format: "json"
			},
		]
	} @dagger(output)
}
