package core

import (
	"context"
	"os"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

var platformToUname = map[string]string{
	"linux/amd64": "x86_64",
	"linux/arm64": "aarch64",
	"linux/s390x": "s390x",
}

var platformToFileArch = map[string]string{
	"linux/amd64": "x86-64",
	"linux/arm64": "aarch64",
	"linux/s390x": "IBM S/390",
}

func TestPlatformEmulatedExecAndPush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	require.NoError(t, err)
	defer c.Close()

	startRegistry(ctx, c, t)

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

func TestPlatformCrossCompile(t *testing.T) {
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

	var hostPlatform string
	variants := make([]dagger.ContainerID, 0, len(platformToFileArch))
	for platform := range platformToUname {
		ctr := c.Container().
			From("crazymax/goxx:latest").
			WithMountedDirectory("/src", thisRepo).
			WithMountedDirectory("/out", "").
			WithWorkdir("/src").
			WithEnvVariable("TARGETPLATFORM", platform).
			Exec(dagger.ContainerExecOpts{
				Args: []string{"sh", "-c", "uname -m && goxx-go build -o /out/cloak /src/cmd/cloak"},
			})

		// TODO: add API for retrieving platform of buildkit host?
		// TODO: until then, just assert that the platform is at least not changing in this case
		stdout, err := ctr.Stdout().Contents(ctx)
		require.NoError(t, err)
		stdout = strings.TrimSpace(stdout)
		if hostPlatform == "" {
			hostPlatform = stdout
		} else {
			require.Equal(t, hostPlatform, stdout)
		}

		dirID, err := ctr.
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

func TestPlatformInvalid(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := dagger.Connect(ctx,
		dagger.WithLogOutput(os.Stdout),
	)
	require.NoError(t, err)
	defer c.Close()

	_, err = c.Container(dagger.ContainerOpts{Platform: "windows98"}).ID(ctx)
	require.ErrorContains(t, err, "unknown operating system or architecture")
}
