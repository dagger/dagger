package main

import (
	"github.com/dagger/cloak/dagger"
)

func main() {
	netlify := dagger.New()
	netlify.Action("deploy", func(ctx *dagger.Context, input *dagger.Input) (*dagger.Output, error) {
		if err := dagger.DummyRun(ctx, "echo netlify deploy"); err != nil {
			return nil, err
		}

		output := &dagger.Output{}
		output.Set(map[string]string{"url": "https://dagger.io/"})
		return output, nil
	})

	if err := netlify.Serve(); err != nil {
		panic(err)
	}
}
