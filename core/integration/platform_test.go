package core

import (
	"context"
	"fmt"
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

// TODO: speed up test w/ parallelism
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

// TODO: speed up test w/ parallelism, cache mount?
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

	// cross compile the cloak binary for each platform
	var hostPlatform string
	variants := make([]dagger.ContainerID, 0, len(platformToFileArch))
	for platform := range platformToUname {
		ctr := c.Container().
			From("crazymax/goxx:latest").
			WithMountedDirectory("/src", thisRepo).
			WithMountedDirectory("/out", "").
			WithWorkdir("/src").
			WithEnvVariable("TARGETPLATFORM", platform).
			WithEnvVariable("CGO_ENABLED", "0").
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

	// make sure the binaries for each platform are executable via emulation now
	for _, id := range variants {
		exit, err := c.Container(dagger.ContainerOpts{ID: id}).
			Exec(dagger.ContainerExecOpts{
				Args: []string{"/cloak", "version"},
			}).
			ExitCode(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, exit)
	}

	// push a multiplatform image
	testRef := "127.0.0.1:5000/testmultiplatimagepush:latest"
	_, err = c.Container().Publish(ctx, testRef, dagger.ContainerPublishOpts{
		PlatformVariants: variants,
	})
	require.NoError(t, err)

	// pull the images, mount them all into a container and ensure the binaries are the right platform
	ctr := c.Container().From("alpine:3.16").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"apk", "add", "file"},
		})
	var cmds []string
	for platform, uname := range platformToFileArch {
		pulledDirID, err := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(testRef).
			FS().
			ID(ctx)
		require.NoError(t, err)
		ctr = ctr.WithMountedDirectory("/"+platform, pulledDirID)
		cmds = append(cmds, fmt.Sprintf(`file /%s/cloak | grep '%s'`, platform, uname))
	}
	exit, err := ctr.
		Exec(dagger.ContainerExecOpts{
			Args: []string{"sh", "-c", strings.Join(cmds, " && ")},
		}).
		ExitCode(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, exit)
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
