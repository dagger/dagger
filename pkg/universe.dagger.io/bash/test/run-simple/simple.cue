package bash

import (
	"dagger.io/dagger"

	"universe.dagger.io/docker"
	"universe.dagger.io/bash"
)

dagger.#DAG & {
	actions: {
		"Run from source directory": {
			run: bash.#Run & {
				source:   loadScripts.output
				filename: "hello.sh"
				container: export: files: "/out.txt": _
			}
			output: run.container.export.files["/out.txt"].contents & "Hello, world\n"
		}

		"Run from string": {
			run: bash.#Run & {
				script: "echo 'Hello, inlined world!' > /output.txt"
				container: export: files: "/output.txt": _
			}
			output: run.container.export.files["/output.txt"].contents & "Hello, inlined world!\n"
		}

		"Run from string with custom image": {
			debian: docker.#Pull & {
				source: "index.docker.io/debian"
			}
			run: bash.#Run & {
				script: "echo 'Hello, inlined world!' > /output.txt"
				container: export: files: "/output.txt": _
				container: image: debian.output
			}
			output: run.container.export.files["/output.txt"].contents & "Hello, inlined world!\n"
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
			output: run.export.files["/out.txt"].contents & "Hello, world\n"
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
