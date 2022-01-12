package main

import (
	"dagger.io/dagger/engine"
	"testing.dagger.io/hidden"
)

engine.#Plan & {
	actions: {
		pull: engine.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}
		_write: engine.#WriteFile & {
			input:       pull.output
			path:        "/testing"
			contents:    "1,2,3"
			permissions: 700
		}
		readfile: engine.#ReadFile & {
			input: _write.output
			path:  "/testing"
		} & {
			// assert result
			contents: "1,2,3"
		}
		hiddenTasks: hidden.#Hidden & {
			// assert result
			contents: "1,2,3"
		}
	}
}
