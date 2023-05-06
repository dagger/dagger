package main

import (
	"dagger.io/dagger"
)

func main() {
	dagger.Serve(CI)
}

func CI(ctx dagger.Context, repo, branch string) (CITargets, error) {
	return CITargets{
		SrcDir: ctx.Client().Git(repo).Branch(branch).Tree(),
	}, nil
}

type CITargets struct {
	SrcDir *dagger.Directory
}

// Dagger SDK targets
func (t CITargets) SDK(ctx dagger.Context) (SDKTargets, error) {
	return SDKTargets(t), nil
}

type SDKTargets struct {
	SrcDir *dagger.Directory
}

// Dagger Engine targets
func (t CITargets) Engine(ctx dagger.Context) (EngineTargets, error) {
	return EngineTargets(t), nil
}

type EngineTargets struct {
	SrcDir *dagger.Directory
}
