package main

import (
	"dagger.io/dagger"
)

func main() {
	dagger.Serve(CI)
}

func CI(ctx dagger.Context, repo, branch string) (Targets, error) {
	// TODO: add support for "default" target and make this it
	return Targets{
		// TODO: passing SrcDir will be replaced with Project API
		SrcDir: ctx.Client().Git(repo).Branch(branch).Tree(),
	}, nil
}

type Targets struct {
	SrcDir *dagger.Directory
}

func (t Targets) SDK(ctx dagger.Context) (SDK, error) {
	return SDK(t), nil
}

type SDK struct {
	SrcDir *dagger.Directory
}
