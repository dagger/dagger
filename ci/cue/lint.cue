package cue

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
	"universe.dagger.io/bash"
)

#Lint: {
	source: dagger.#FS

	docker.#Build & {
		steps: [
			alpine.#Build & {
				packages: bash: _
				packages: curl: _
				packages: git:  _
			},

			docker.#Copy & {
				contents: source
				"source": "go.mod"
				dest:     "go.mod"
			},

			// Install CUE
			bash.#Run & {
				script: contents: #"""
					        export CUE_VERSION="$(grep cue ./go.mod | cut -d' ' -f2 | head -1 | sed -E 's/\.[[:digit:]]\.[[:alnum:]]+-[[:alnum:]]+$//')"
					        export CUE_TARBALL="cue_${CUE_VERSION}_linux_amd64.tar.gz"
					        echo "Installing cue version $CUE_VERSION"
					        curl -L "https://github.com/cue-lang/cue/releases/download/${CUE_VERSION}/${CUE_TARBALL}" | tar zxf - -C /usr/local/bin
					        cue version
					"""#
			},

			// CACHE: copy only *.cue files
			docker.#Copy & {
				contents: source
				include: [".git", "*.cue", "**/*.cue"]
				dest: "/cue"
			},

			// LINT
			bash.#Run & {
				workdir: "/cue"
				script: contents: #"""
					git status

					find . -name '*.cue' -not -path '*/cue.mod/*' -print | time xargs -t -n 1 -P 8 cue fmt -s
					modified="$(git status -s . | grep -e "^ M"  | grep "\.cue" | cut -d ' ' -f3 || true)"
					test -z "$modified" || (echo -e "linting error in:\n${modified}" > /dev/stderr ; false)
					"""#
			},
		]
	}
}
