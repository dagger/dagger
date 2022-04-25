package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

// BEFORE: failed to execute plan: task failed: actions.test._op: actions.test._op.source: non-concrete value string
//  AFTER: failed to load plan: "actions.test._ref" is not concrete: string

#Test: {
	required: string
	_op:      core.#Pull & {
		source: required
	}
}

dagger.#Plan & {
	actions: test: #Test & {
		_ref:     string
		required: _ref
	}
}
