package git

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/alpine"
)

// A git repository
#Repository: {

	remote: string       @dagger(input)
	ref:    string       @dagger(input)
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

// Get the name of the current checked out branch or tag
#CurrentBranch: {
	repository: dagger.#Artifact @dagger(input)
	name: {
		string
		@dagger(output)

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
	}
}

// List tags of a repository
#Tags: {
	repository: dagger.#Artifact @dagger(input)
	tags: {
		[...string]
		@dagger(output)

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
	}
}
