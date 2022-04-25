package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

// BEFORE: failed to execute plan: task failed: actions.test._op: actions.test._op.source: non-concrete value string
//  AFTER: failed to load plan: "actions.test.required" is not concrete: string

#Test: {
	required: dagger.#FS
	_op:      core.#WriteFile & {
		input:    required
		path:     "/test"
		contents: "test"
	}
}

dagger.#Plan & {
	actions: test: #Test & {}
}
