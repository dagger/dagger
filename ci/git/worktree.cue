package git

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
	"universe.dagger.io/bash"
)

#Worktree: {
	source: dagger.#FS

	_img: docker.#Build & {
		steps: [
			alpine.#Build & {
				packages: bash: _
				packages: git:  _
			},

			docker.#Copy & {
				contents: source
				dest:     "/cue"
			},
		]
	}

	// Create Git repository for worktree folders
	_container: bash.#Run & {
		input:   _img.output
		workdir: "/cue"
		script: contents: #"""
			# `.git` directory not present in git worktrees
			if [[ ! -d ".git" ]]; then
				rm .git
				git init --quiet
				git config --global user.name 'Dagger Linter'
				git config --global user.email '<>'
				git add -A 1>/dev/null 2>/dev/null
				git commit -m "Initial linter commit" --quiet
			fi
			"""#
		export: directories: "/cue": _
	}

	output: _container.export.directories."/cue"
}
