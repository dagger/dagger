package main

import (
	"strings"
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: commands: rev: {
		// Print stdin in reverse
		// Same as `rev <(echo olleh)` or `echo olleh | rev`
		name:  "rev"
		stdin: "olleh"
	}

	actions: test: {
		_op: core.#Nop & {
			input: strings.TrimSpace(client.commands.rev.stdout)
		}
		verify: _op.output & "hello"
	}
}
