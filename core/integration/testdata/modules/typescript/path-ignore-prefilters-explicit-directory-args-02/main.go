package main

import (
	"dagger/test-mod/internal/dagger"
)

type TestMod struct{}

func (t *TestMod) Test(
	dir *dagger.Directory,
) *dagger.Directory {
	return dag.Test().Call(dir)
}
