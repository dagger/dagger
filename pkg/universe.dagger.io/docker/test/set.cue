package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		image: output: docker.#Image & {
			rootfs: engine.#Scratch
			config: {
				cmd: ["/bin/sh"]
				env: PATH: "/sbin:/bin"
				onbuild: ["COPY . /app"]
			}
		}
		set: docker.#Set & {
			input: image.output
			config: {
				env: FOO: "bar"
				workdir: "/root"
				onbuild: ["RUN /app/build.sh"]
			}
		}
		verify: set.output.config & {
			env: {
				PATH: "/sbin:/bin"
				FOO:  "bar"
			}
			cmd: ["/bin/sh"]
			workdir: "/root"
			onbuild: [
				"COPY . /app",
				"RUN /app/build.sh",
			]
		}
	}
}
