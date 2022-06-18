//go:generate go run ../../stub -m ./sdk/core/model.gen.go -f ./main.gen.go
package main

import (
	"github.com/dagger/cloak/dagger"
	"github.com/dagger/cloak/examples/core/sdk/core"
	"github.com/moby/buildkit/client/llb"
)

func Image(ctx *dagger.Context, i *core.ImageInput) *core.ImageOutput {
	fs, err := dagger.Solve(ctx, llb.Image(i.Ref))
	if err != nil {
		panic(err)
	}
	return &core.ImageOutput{FS: fs}
}

func Git(ctx *dagger.Context, i *core.GitInput) *core.GitOutput {
	fs, err := dagger.Solve(ctx, llb.Git(i.Remote, i.Ref))
	if err != nil {
		panic(err)
	}
	return &core.GitOutput{FS: fs}
}

func Exec(ctx *dagger.Context, i *core.ExecInput) *core.ExecOutput {
	execState := toState(i.FS).Run(
		llb.Dir(i.Dir),
		llb.Args(i.Args),
	)
	mntStates := make(map[string]llb.State)
	for path, fs := range i.Mounts {
		mntState := execState.AddMount(path, toState(fs))
		mntStates[path] = mntState
	}

	rootFS, err := dagger.Solve(ctx, execState.Root())
	if err != nil {
		panic(err)
	}
	mntFSs := make(map[string]dagger.FS)
	for path, mntState := range mntStates {
		mntFS, err := dagger.Solve(ctx, mntState)
		if err != nil {
			panic(err)
		}
		mntFSs[path] = mntFS
	}
	mntFSs["/"] = rootFS
	return &core.ExecOutput{FS: rootFS, Mounts: mntFSs}
}

func toState(fs dagger.FS) llb.State {
	defop, err := llb.NewDefinitionOp(fs.Def)
	if err != nil {
		panic(err)
	}
	return llb.NewState(defop)
}
