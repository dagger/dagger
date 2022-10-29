package core

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

var platformToUname = map[dagger.Platform]string{
	"linux/amd64": "x86_64",
	"linux/arm64": "aarch64",
	"linux/s390x": "s390x",
}

var platformToFileArch = map[dagger.Platform]string{
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

		ctrPlatform, err := ctr.Platform(ctx)
		require.NoError(t, err)
		require.Equal(t, platform, ctrPlatform)

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

// TODO: speed up test w/ parallelism
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

	gomodCache, err := c.CacheVolume("gomod").ID(ctx)
	require.NoError(t, err)

	gobuildCache, err := c.CacheVolume("gobuild").ID(ctx)
	require.NoError(t, err)

	// cross compile the cloak binary for each platform
	defaultPlatform, err := c.DefaultPlatform(ctx)
	require.NoError(t, err)
	variants := make([]dagger.ContainerID, 0, len(platformToFileArch))
	for platform := range platformToUname {
		ctr := c.Container().
			From("crazymax/goxx:latest").
			WithMountedCache(gomodCache, "/go/pkg/mod").
			WithMountedCache(gobuildCache, "/root/.cache/go-build").
			WithMountedDirectory("/src", thisRepo).
			WithMountedDirectory("/out", "").
			WithWorkdir("/src").
			WithEnvVariable("TARGETPLATFORM", string(platform)).
			WithEnvVariable("CGO_ENABLED", "0").
			Exec(dagger.ContainerExecOpts{
				Args: []string{"sh", "-c", "uname -m && goxx-go build -o /out/cloak /src/cmd/cloak"},
			})

		// should be running as the default (buildkit host) platform
		ctrPlatform, err := ctr.Platform(ctx)
		require.NoError(t, err)
		require.Equal(t, defaultPlatform, ctrPlatform)

		stdout, err := ctr.Stdout().Contents(ctx)
		require.NoError(t, err)
		stdout = strings.TrimSpace(stdout)
		require.Equal(t, platformToUname[defaultPlatform], stdout)

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
	cmds := make([]string, 0, len(platformToFileArch))
	for platform, uname := range platformToFileArch {
		pulledDirID, err := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(testRef).
			FS().
			ID(ctx)
		require.NoError(t, err)
		ctr = ctr.WithMountedDirectory("/"+string(platform), pulledDirID)
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

func TestPlatformCacheMounts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := dagger.Connect(ctx,
		dagger.WithLogOutput(os.Stdout),
	)
	require.NoError(t, err)
	defer c.Close()

	randomID := identity.NewID()

	cacheID, err := c.CacheVolume("test-platform-cache-mount").ID(ctx)
	require.NoError(t, err)

	// make sure cache mounts are inherently platform-agnostic
	cmds := make([]string, 0, len(platformToUname))
	for platform := range platformToUname {
		exit, err := c.Container(dagger.ContainerOpts{Platform: platform}).
			From("alpine:3.16").
			WithMountedCache(cacheID, "/cache").
			Exec(dagger.ContainerExecOpts{
				Args: []string{"sh", "-x", "-c", strings.Join([]string{
					"mkdir -p /cache/" + randomID + string(platform),
					"uname -m > /cache/" + randomID + string(platform) + "/uname",
				}, " && ")},
			}).
			ExitCode(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, exit)
		cmds = append(cmds, fmt.Sprintf(`cat /cache/%s%s/uname | grep '%s'`, randomID, platform, platformToUname[platform]))
	}

	exit, err := c.Container().
		From("alpine:3.16").
		WithMountedCache(cacheID, "/cache").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"sh", "-x", "-c", strings.Join(cmds, " && ")},
		}).
		ExitCode(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, exit)
}

// TODO: test that exec on darwin/windows fails reasonably
// TODO: test you can cross-compile and local export darwin/windows binaries
