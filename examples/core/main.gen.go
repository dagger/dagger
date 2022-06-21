package main

import (
	"github.com/dagger/cloak/dagger"

	// TODO: need more generic mechanism for generating this import
	"github.com/dagger/cloak/examples/core/sdk/core"
)

func main() {
	d := dagger.New()

	d.Action("image", func(ctx *dagger.Context, input []byte) ([]byte, error) {
		typedInput := &core.ImageInput{}
		if err := dagger.UnmarshalBytes(ctx, input, typedInput); err != nil {
			return nil, err
		}
		typedOutput := Image(ctx, typedInput)
		return dagger.MarshalBytes(ctx, typedOutput)
	})
	d.Action("git", func(ctx *dagger.Context, input []byte) ([]byte, error) {
		typedInput := &core.GitInput{}
		if err := dagger.UnmarshalBytes(ctx, input, typedInput); err != nil {
			return nil, err
		}
		typedOutput := Git(ctx, typedInput)
		return dagger.MarshalBytes(ctx, typedOutput)
	})
	d.Action("exec", func(ctx *dagger.Context, input []byte) ([]byte, error) {
		typedInput := &core.ExecInput{}
		if err := dagger.UnmarshalBytes(ctx, input, typedInput); err != nil {
			return nil, err
		}
		typedOutput := Exec(ctx, typedInput)
		return dagger.MarshalBytes(ctx, typedOutput)
	})

	if err := d.Serve(); err != nil {
		panic(err)
	}
}
