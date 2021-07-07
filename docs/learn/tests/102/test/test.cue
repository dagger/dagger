package multibucket

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/git"
)

// Collect website from git repo
// Override source.cue Input
src: git.#Repository & {
	remote: "https://github.com/dagger/examples"
	ref:    "main"
	subdir: "todoapp"
}

TestNetlify: {

	// Check if the deployed site has the random marker
	check: #up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash: "=~5.1"
				package: curl: true
			}
		},
		op.#Exec & {
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"-c",
				#"""
            # Check that `./static/css/main.9149988f.chunk.css` substr is present in curl
            [[ "$(curl \#(site.netlify.deployUrl))" == *"./static/css/main.9149988f.chunk.css"* ]]
        """#,
			]
		},
	]
}
