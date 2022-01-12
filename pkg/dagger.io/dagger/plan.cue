package dagger

import (
	"alpha.dagger.io/europa/dagger/engine"
)

// A deployment plan executed by `dagger up`
#Plan: engine.#Plan

// A special kind of program which `dagger` can execute.
#DAG: engine.#DAG
