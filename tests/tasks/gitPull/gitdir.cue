package testing

import "alpha.dagger.io/europa/dagger/engine"

engine.#Plan & {
	actions: {
		repo1: engine.#GitPull & {
			remote: "https://github.com/blocklayerhq/acme-clothing.git"
			ref:    "master"
		}

		repo2: engine.#GitPull & {
			remote:     "https://github.com/blocklayerhq/acme-clothing.git"
			ref:        "master"
			keepGitDir: true
		}

		image: engine.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		verify: engine.#Exec & {
			input: image.output
			args: ["sh", "-c", """
				set -eu
				[ ! -d /repo1/.git ]
				[ -d /repo2/.git ]
				"""]
			mounts: {
				repo_1: {dest: "/repo1", contents: repo1.output}
				repo_2: {dest: "/repo2", contents: repo2.output}
			}
		}

	}
}
