package bash

import (
	"dagger.io/dagger"

	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

dagger.#DAG & {
	actions: {
		"Run from source directory": {
			build: alpine.#Build & {
				packages: bash: _
			}
			run: #Run & {
				image: build.output
				script: {
					directory: loadScripts.output
					filename:  "hello.sh"
				}
				export: files: "/out.txt": _
			}
			output: run.export.files."/out.txt".contents & "Hello, world\n"
		}

		"Run from source directory with custom image": {
			debian: docker.#Pull & {
				source: "index.docker.io/debian"
			}
			run: #Run & {
				image: debian.output
				export: files: "/out.txt": _
				script: {
					directory: loadScripts.output
					filename:  "hello.sh"
				}
			}
			output: run.export.files."/out.txt".contents & "Hello, world\n"
		}

		"Run from string": {
			run: #Run & {
				script: contents: "echo 'Hello, inlined world!' > /output.txt"
				export: files: "/output.txt": _
			}
			output: run.export.files."/output.txt".contents & "Hello, inlined world!\n"
		}

		"Run from string with custom image": {
			debian: docker.#Pull & {
				source: "index.docker.io/debian"
			}
			run: #Run & {
				image: debian.output
				export: files: "/output.txt": _
				script: contents: "echo 'Hello, inlined world!' > /output.txt"
			}
			output: run.export.files."/output.txt".contents & "Hello, inlined world!\n"
		}

		// Same thing but without bash.#Run
		control: {
			run: docker.#Run & {
				image: base.output
				command: {
					name: "sh"
					args: ["/bash/scripts/hello.sh"]
				}
				mounts: scripts: {
					contents: loadScripts.output
					dest:     "/bash/scripts"
				}
				export: files: "/out.txt": _
			}
			output: run.export.files."/out.txt".contents & "Hello, world\n"
			base:   docker.#Pull & {
				source: "alpine"
			}
		}

		loadScripts: dagger.#Source & {
			path: "."
			include: ["*.sh"]
		}
	}
}
