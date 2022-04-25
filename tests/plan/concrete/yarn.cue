package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/yarn"
)

// BEFORE: failed to execute plan: task failed: actions.test._exec: invalid FS at path "actions.test._exec.input": FS is not set
//  AFTER: failed to load plan: "actions.test.input" is not set:
//    ../../cue.mod/pkg/universe.dagger.io/docker/run.cue:13:2

dagger.#Plan & {
	actions: test: yarn.#Run
}
