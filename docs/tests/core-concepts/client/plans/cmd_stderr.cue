package main

import (
	"strings"
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: commands: cat: {
		name: "sh"
		// simulate error output without failed exit status
		flags: "-c": """
			cat /foobar
			echo ok
			"""
	}

	actions: test: {
		_op: core.#Nop & {
			input: strings.TrimSpace(client.commands.cat.stderr)
		}
		error: _op.output
	}
}
