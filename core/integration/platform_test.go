package core

import (
	"context"
	"os"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestContainerPlatformEmulatedExecAndPush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	require.NoError(t, err)
	defer c.Close()

	startRegistry(ctx, c, t)

	platformToUname := map[string]string{
		"linux/amd64": "x86_64",
		"linux/arm64": "aarch64",
		"linux/s390x": "s390x",
	}

	variants := make([]dagger.ContainerID, 0, len(platformToUname))
	for platform, uname := range platformToUname {
		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From("alpine:3.16.2").
			Exec(dagger.ContainerExecOpts{
				Args: []string{"uname", "-m"},
			})
		output, err := ctr.Stdout().Contents(ctx)
		require.NoError(t, err)
		output = strings.TrimSpace(output)
		require.Equal(t, uname, output)

		id, err := ctr.ID(ctx)
		require.NoError(t, err)
		variants = append(variants, id)
	}

	testRef := "127.0.0.1:5000/testmultiplatimagepush:latest"
	_, err = c.Container().Publish(ctx, testRef, dagger.ContainerPublishOpts{
		PlatformVariants: variants,
	})
	require.NoError(t, err)

	for platform, uname := range platformToUname {
		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(testRef).
			Exec(dagger.ContainerExecOpts{
				Args: []string{"uname", "-m"},
			})
		output, err := ctr.Stdout().Contents(ctx)
		require.NoError(t, err)
		output = strings.TrimSpace(output)
		require.Equal(t, uname, output)
	}
}

func TestContainerPlatformCrossCompile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := dagger.Connect(ctx,
		dagger.WithWorkdir("../.."),
		dagger.WithLogOutput(os.Stdout),
	)
	require.NoError(t, err)
	defer c.Close()

	startRegistry(ctx, c, t)

	thisRepo, err := c.Host().Workdir().ID(ctx)
	require.NoError(t, err)

	platformToFileArch := map[string]string{
		"linux/amd64": "x86-64",
		"linux/arm64": "aarch64",
		"linux/s390x": "IBM S/390",
	}

	variants := make([]dagger.ContainerID, 0, len(platformToFileArch))
	for platform := range platformToFileArch {
		dirID, err := c.Container().
			From("crazymax/goxx:latest").
			WithMountedDirectory("/src", thisRepo).
			WithMountedDirectory("/out", "").
			WithWorkdir("/src").
			WithEnvVariable("TARGETPLATFORM", platform).
			Exec(dagger.ContainerExecOpts{
				Args: []string{"goxx-go", "build", "-o", "/out/cloak", "/src/cmd/cloak"},
			}).
			Directory("/out").
			ID(ctx)
		require.NoError(t, err)

		id, err := c.Container(dagger.ContainerOpts{Platform: platform}).WithFS(dirID).ID(ctx)
		require.NoError(t, err)
		variants = append(variants, id)
	}

	testRef := "127.0.0.1:5000/testmultiplatimagepush:latest"
	_, err = c.Container().Publish(ctx, testRef, dagger.ContainerPublishOpts{
		PlatformVariants: variants,
	})
	require.NoError(t, err)

	for platform, uname := range platformToFileArch {
		pulledDirID, err := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(testRef).
			FS().
			ID(ctx)
		require.NoError(t, err)

		output, err := c.Container().From("alpine:3.16").
			Exec(dagger.ContainerExecOpts{
				Args: []string{"apk", "add", "file"},
			}).
			WithMountedDirectory("/mnt", pulledDirID).
			Exec(dagger.ContainerExecOpts{
				Args: []string{"file", "/mnt/cloak"},
			}).
			Stdout().Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, output, uname)
	}
}
