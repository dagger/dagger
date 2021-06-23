package main

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
	"alpha.dagger.io/alpine"
)

// Assert that there are no errors
err: ""

// Directory containing universe packages (./universe/ in dagger repo)
universe: dagger.#Input & dagger.#Artifact

ctr: #CueCLI & {
	vendor: "alpha.dagger.io": universe
	command: """
		(
		find . -name '*.cue' -print0 | xargs -0iX dirname X | sort -u | {

			while read -r dir; do
				echo "--- $dir"
				cue eval "$dir" >/dev/null
			done
		} > /out 2>/err
		) || true
		"""
}

result: (os.#File & {
		from: ctr.ctr
		path: "/out"
}).contents @dagger(output)

err: (os.#File & {
		from: ctr.ctr
		path: "/err"
}).contents @dagger(output)

#CueCLI: {
	command: string
	vendor: [name=string]: dagger.#Artifact

	ctr: os.#Container & {
		image: alpine.#Image & {
			package: {
				curl: true
				tar:  true
			}
		}
		setup: [
			"""
				set -e
				cd $(mktemp -d)
				curl -L https://github.com/cuelang/cue/releases/download/v0.4.0/cue_v0.4.0_linux_amd64.tar.gz -o cue.tgz
				tar zxvf cue.tgz
				cp cue /usr/local/bin/cue
				rm -fr ./*
				""",
		]
		for name, dir in vendor {
			mount: "/ctx/cue.mod/pkg/\(name)": from: dir
		}
		dir:       "/ctx"
		"command": command
	}
}
