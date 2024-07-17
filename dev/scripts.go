package main

import (
	"context"
)

type Scripts struct {
	Dagger *DaggerDev // +private
}

// Lint scripts files
func (s Scripts) Lint(ctx context.Context) error {
	return dag.Shellcheck().
		Check(s.Dagger.Source.File("install.sh")).
		Assert(ctx)
}
