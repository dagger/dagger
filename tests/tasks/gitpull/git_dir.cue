package testing

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: {
		repo1: dagger.#GitPull & {
			remote: "https://github.com/blocklayerhq/acme-clothing.git"
			ref:    "master"
		}

		repo2: dagger.#GitPull & {
			remote:     "https://github.com/blocklayerhq/acme-clothing.git"
			ref:        "master"
			keepGitDir: true
		}

		image: dagger.#Pull & {
			source: "alpine:3.15.0"
		}

		verify: dagger.#Exec & {
			input: image.output
			args: ["sh", "-c", """
				set -eu
				[ ! -d /repo1/.git ]
				[ -d /repo2/.git ]
				"""]
			mounts: {
				a: {dest: "/repo1", contents: repo1.output}
				b: {dest: "/repo2", contents: repo2.output}
			}
		}

	}
}
