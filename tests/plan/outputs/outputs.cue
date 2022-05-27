package main

import (
	"strings"
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: test: {
		simple: #Pull

		// no outputs
		empty: {
			pull: #Pull
		}

		// dependency
		dep: core.#Nop & {
			input: "hello world"
		}

		// control which outputs you want
		control: {
			_pull: #Pull

			// simple reference
			foo: _pull.digest

			// extended reference
			bar: core.#Ref & _pull.digest

			// dynamic
			if _pull.config.cmd != _|_ {
				cmd: _pull.config.cmd[0]
			}

			// transformation
			transf: strings.TrimSpace("\(foo)\n")

			// non-string
			_notString: core.#Nop & {
				input: 42
			}
			int: _notString.output

			// non-scalars not supported
			config: _pull.config

			// outside dependency references not supported
			outRef: dep.output

			// should skip inputs
			input:    string | *"foobar"
			inputRef: core.#Ref & "foobar"
		}
	}
}

#Pull: core.#Pull & {
	source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
}
