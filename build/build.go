package main

import (
	"runtime"

	"dagger.io/dagger"
	"github.com/goyek/goyek/v2"
	"github.com/stretchr/testify/require"
)

var _ = goyek.Define(goyek.Task{
	Name:  "build",
	Usage: "Build builds the binary",
	Action: func(tf *goyek.TF) {
		ctx := tf.Context()
		c := daggerClient(tf)
		defer c.Close()

		workdir := c.Host().Workdir()
		builder := c.Container().
			From("golang:1.19-alpine").
			WithEnvVariable("CGO_ENABLED", "0").
			WithEnvVariable("GOOS", runtime.GOOS).
			WithEnvVariable("GOARCH", runtime.GOARCH).
			WithWorkdir("/app")

		// install dependencies
		modules := c.Directory()
		for _, f := range []string{"go.mod", "go.sum", "sdk/go/go.mod", "sdk/go/go.sum"} {
			fileID, err := workdir.File(f).ID(ctx)
			require.NoError(tf, err)

			modules = modules.WithCopiedFile(f, fileID)
		}
		modID, err := modules.ID(ctx)
		require.NoError(tf, err)
		builder = builder.
			WithMountedDirectory("/app", modID).
			Exec(dagger.ContainerExecOpts{
				Args: []string{"go", "mod", "download"},
			})

		src, err := workdir.ID(ctx)
		require.NoError(tf, err)

		builder = builder.
			WithMountedDirectory("/app", src).WithWorkdir("/app").
			Exec(dagger.ContainerExecOpts{
				Args: []string{"go", "build", "-o", "./bin/cloak", "-ldflags", "-s -w", "/app/cmd/cloak"},
			})

		ok, err := builder.Directory("./bin").Export(ctx, "./bin")
		require.NoError(tf, err)
		require.True(tf, ok, "HostDirectoryWrite not ok")
	},
})
