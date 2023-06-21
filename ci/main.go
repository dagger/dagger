package main

import (
	"dagger.io/dagger"
)

func main() {
	dagger.ServeCommands(CI)
}

// Dagger CI targets
func CI(ctx dagger.Context, repo, branch string) (CITargets, error) {
	srcDir := ctx.Client().Host().Directory(".")
	if repo != "" {
		srcDir = ctx.Client().Git(repo).Branch(branch).Tree()
	}
	return CITargets{
		SrcDir: srcDir,
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
