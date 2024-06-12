package main

import (
	"context"
)

type Scripts struct {
	Dagger *Dagger // +private
}

// Lint scripts files
func (s Scripts) Lint(ctx context.Context) error {
	_, err := dag.Shellcheck().
		Check(s.Dagger.Source.File("install.sh")).
		Assert(ctx)
	return err
}
