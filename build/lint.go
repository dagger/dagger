package main

import (
	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator"
	"github.com/goyek/goyek/v2"
	"github.com/stretchr/testify/require"
)

var _ = goyek.Define(goyek.Task{
	Name:  "lint",
	Usage: "All runs all lint targets",
	Deps: goyek.Deps{
		codegen,
		markdown,
	},
})

var markdown = goyek.Define(goyek.Task{
	Name:  "lint:markdown",
	Usage: "Markdown lints the markdown files",
	Action: func(tf *goyek.TF) {
		ctx := tf.Context()
		c := daggerClient(tf)
		defer c.Close()

		workdir := c.Host().Workdir()

		src, err := workdir.ID(ctx)
		require.NoError(tf, err)

		cfg, err := workdir.File(".markdownlint.yaml").ID(ctx)
		require.NoError(tf, err)

		_, err = c.Container().
			From("tmknom/markdownlint:0.31.1").
			WithMountedDirectory("/src", src).
			WithMountedFile("/src/.markdownlint.yaml", cfg).
			WithWorkdir("/src").
			Exec(dagger.ContainerExecOpts{
				Args: []string{
					"-c",
					".markdownlint.yaml",
					"--",
					"./docs",
					"README.md",
				},
			}).ExitCode(ctx)
		require.NoError(tf, err)
	},
})

var codegen = goyek.Define(goyek.Task{
	Name:  "lint:codegen",
	Usage: "Codegen ensure the SDK code was re-generated",
	Action: func(tf *goyek.TF) {
		ctx := tf.Context()
		c := daggerClient(tf)
		defer c.Close()

		generated, err := generator.IntrospectAndGenerate(ctx, c, generator.Config{
			Package: "dagger",
		})
		require.NoError(tf, err)

		// grab the file from the repo
		src, err := c.
			Host().
			Workdir().
			File("sdk/go/api.gen.go").
			Contents(ctx)
		require.NoError(tf, err)

		// compare the two
		require.Equal(tf, src, string(generated), "generated api mismatch. please run `go generate ./...")
	},
})
