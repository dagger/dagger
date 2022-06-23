//go:generate go run ../../stub -m ./sdk/core/model.gen.go -f ./main.gen.go
package main

import (
	"context"

	"github.com/dagger/cloak/dagger"
	"github.com/dagger/cloak/examples/core/sdk/core"
	"github.com/moby/buildkit/client/llb"
)

func Image(ctx *dagger.Context, i *core.ImageInput) *core.ImageOutput {
	llbdef, err := llb.Image(i.Ref.Evaluate(ctx)).Marshal(context.TODO())
	if err != nil {
		panic(err)
	}
	fs, err := dagger.NewFS(ctx, llbdef.ToPB())
	if err != nil {
		panic(err)
	}
	return &core.ImageOutput{FS: *fs}
}

func Git(ctx *dagger.Context, i *core.GitInput) *core.GitOutput {
	llbdef, err := llb.Git(i.Remote.Evaluate(ctx), i.Ref.Evaluate(ctx)).Marshal(context.TODO())
	if err != nil {
		panic(err)
	}
	fs, err := dagger.NewFS(ctx, llbdef.ToPB())
	if err != nil {
		panic(err)
	}
	return &core.GitOutput{FS: *fs}
}

func Exec(ctx *dagger.Context, i *core.ExecInput) *core.ExecOutput {
	execState := toState(ctx, i.FS).Run(
		llb.Dir(i.Dir.Evaluate(ctx)),
		llb.Args(dagger.Strings(i.Args).Evaluate(ctx)),
	)
	for _, mnt := range i.Mounts {
		execState.AddMount(mnt.Path.Evaluate(ctx), toState(ctx, mnt.FS))
	}

	execStateDef, err := execState.Marshal(context.TODO())
	if err != nil {
		panic(err)
	}
	rootFS, err := dagger.NewFS(ctx, execStateDef.ToPB())
	if err != nil {
		panic(err)
	}
	return &core.ExecOutput{FS: *rootFS}
}

func toState(ctx *dagger.Context, fs dagger.FS) llb.State {
	defop, err := llb.NewDefinitionOp(fs.Definition(ctx))
	if err != nil {
		panic(err)
	}
	return llb.NewState(defop)
}
