package main

import (
	"dagger.io/dagger"
	"dagger.io/universe/netlify"
	"dagger.io/universe/yarn"
)

func main() {
	dagger.Pipeline(func(ctx *dagger.Context) {
		ctx.Action("deploy", func() {
			source := ctx.Client().Filesystem().Directory(".")

			deployment := netlify.Deploy(yarn.Run(source, "build"))

			ctx.Export("url", deployment.URL)
		})
	})
}
