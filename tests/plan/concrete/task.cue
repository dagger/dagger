package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

// BEFORE: failed to execute plan: task failed: actions.test: actions.test.path: non-concrete value string
//  AFTER: failed to load plan: "actions.test.path" is not set:
//     ../../cue.mod/pkg/dagger.io/dagger/core/fs.cue:59:2
// "actions.test.contents" is not set:
//     ../../cue.mod/pkg/dagger.io/dagger/core/fs.cue:61:2

dagger.#Plan & {
	actions: test: core.#WriteFile & {
		input: dagger.#Scratch
	}
}
