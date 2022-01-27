package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		build: engine.#Dockerfile & {
			source: engine.#Scratch
			dockerfile: contents: """
				FROM alpine:3.15.0
				RUN echo -n 'not hello from dagger' > /dagger.txt
				RUN echo '#!/bin/sh' > /bin/dagger
				ENV HELLO_FROM=dagger
				RUN echo 'echo -n "hello from $HELLO_FROM" > /dagger.txt' >> /bin/dagger
				RUN chmod +x /bin/dagger
				WORKDIR /bin
				CMD /bin/dagger
				"""
		}
		myimage: docker.#Image & {
			rootfs: build.output
			config: build.config
		}
		run: docker.#Run & {
			image: myimage
			cmd: name: "ls"
			export: files: {
				"/dagger.txt": _ & {
					contents: "not hello from dagger"
				}
				"/bin/dagger": _ & {
					contents: """
						#!/bin/sh
						echo -n "hello from $HELLO_FROM" > /dagger.txt
						
						"""
				}
			}
		}
		verify_cmd_is_run: docker.#Run & {
			image: myimage
			export: files: "/dagger.txt": _ & {
				contents: "hello from dagger"
			}
		}
		verify_env_is_overridden: docker.#Run & {
			image: myimage
			export: files: "/dagger.txt": _ & {
				contents: "hello from europa"
			}
			env: HELLO_FROM: "europa"
		}

		verify_working_directory: docker.#Run & {
			image: myimage
			script: #"""
				pwd > dir.txt
				"""#
			export: files: "/bin/dir.txt": _ & {
				contents: "/bin\n"
			}
		}
		verify_working_directory_is_overridden: docker.#Run & {
			image:   myimage
			workdir: "/"
			script: #"""
				pwd > dir.txt
				"""#
			export: files: "/dir.txt": _ & {
				contents: "/\n"
			}
		}
	}
}
