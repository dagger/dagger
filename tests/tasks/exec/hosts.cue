package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	actions: {
		image: engine.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		verify: engine.#Exec & {
			input: image.output
			hosts: "unit.test": "1.2.3.4"
			args: [
				"sh", "-c",
				#"""
					grep -q "unit.test" /etc/hosts
					grep -q "1.2.3.4" /etc/hosts
					"""#,
			]
		}
	}
}
