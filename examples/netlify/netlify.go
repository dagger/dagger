package main

import (
	"fmt"

	"github.com/dagger/cloak/dagger"
	"github.com/dagger/cloak/examples/netlify/model"
)

type netlify struct {
}

func (n *netlify) Deploy(ctx *dagger.Context, input *model.DeployInput) (*model.DeployOutput, error) {
	if err := dagger.DummyRun(ctx, "echo netlify deploy"); err != nil {
		return nil, err
	}

	domain := "dagger.io"
	if input.Domain != "" {
		domain = input.Domain
	}

	return &model.DeployOutput{
		URL: fmt.Sprintf("https://%s", domain),
	}, nil
}

func main() {
	if err := model.Serve(&netlify{}); err != nil {
		panic(err)
	}
}
