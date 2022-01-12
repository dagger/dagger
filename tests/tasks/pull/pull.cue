package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	actions: pull: engine.#Pull & {
		source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
	} & {
		// assert result
		digest: "sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		config: {
			Env: ["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"]
			Cmd: ["/bin/sh"]
		}
	}
}
