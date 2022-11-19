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
	"golang.org/x/sync/errgroup"
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

	variants := make([]*dagger.Container, 0, len(platformToUname))
	for platform, uname := range platformToUname {
		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From("alpine:3.16.2").
			Exec(dagger.ContainerExecOpts{
				Args: []string{"uname", "-m"},
			})
		variants = append(variants, ctr)

		ctrPlatform, err := ctr.Platform(ctx)
		require.NoError(t, err)
		require.Equal(t, platform, ctrPlatform)

		output, err := ctr.Stdout(ctx)
		require.NoError(t, err)
		output = strings.TrimSpace(output)
		require.Equal(t, uname, output)
	}

	testRef := "127.0.0.1:5000/testplatformemulatedexecandpush:latest"
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
		output, err := ctr.Stdout(ctx)
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

	// cross compile the dagger binary for each platform
	defaultPlatform, err := c.DefaultPlatform(ctx)
	require.NoError(t, err)
	variants := make([]*dagger.Container, len(platformToFileArch))
	i := 0
	var eg errgroup.Group
	for platform := range platformToUname {
		i++
		i := i - 1
		platform := platform
		eg.Go(func() error {
			ctr := c.Container().
				From("crazymax/goxx:latest").
				WithMountedCache("/go/pkg/mod", c.CacheVolume("gomod")).
				WithMountedCache("/root/.cache/go-build", c.CacheVolume("gobuild")).
				WithMountedDirectory("/src", c.Host().Directory(".")).
				WithMountedDirectory("/out", c.Directory()).
				WithWorkdir("/src").
				WithEnvVariable("TARGETPLATFORM", string(platform)).
				WithEnvVariable("CGO_ENABLED", "0").
				Exec(dagger.ContainerExecOpts{
					Args: []string{"sh", "-c", "uname -m && goxx-go build -o /out/dagger /src/cmd/dagger"},
				})

			// should be running as the default (buildkit host) platform
			ctrPlatform, err := ctr.Platform(ctx)
			require.NoError(t, err)
			require.Equal(t, defaultPlatform, ctrPlatform)

			stdout, err := ctr.Stdout(ctx)
			require.NoError(t, err)
			stdout = strings.TrimSpace(stdout)
			require.Equal(t, platformToUname[defaultPlatform], stdout)

			out := ctr.Directory("/out")
			variants[i] = c.Container(dagger.ContainerOpts{Platform: platform}).WithRootfs(out)
			return nil
		})
	}
	require.NoError(t, eg.Wait())

	// make sure the binaries for each platform are executable via emulation now
	for _, ctr := range variants {
		exit, err := ctr.
			Exec(dagger.ContainerExecOpts{
				Args: []string{"/dagger", "version"},
			}).
			ExitCode(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, exit)
	}

	// push a multiplatform image
	testRef := "127.0.0.1:5000/testplatformcrosscompile:latest"
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
		pulledDir := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(testRef).
			Rootfs()
		ctr = ctr.WithMountedDirectory("/"+string(platform), pulledDir)
		cmds = append(cmds, fmt.Sprintf(`file /%s/dagger | tee /dev/stderr | grep -q '%s'`, platform, uname))
	}
	exit, err := ctr.
		Exec(dagger.ContainerExecOpts{
			Args: []string{"sh", "-x", "-e", "-c", strings.Join(cmds, "\n")},
		}).
		ExitCode(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, exit)
}

func TestPlatformCacheMounts(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := dagger.Connect(ctx,
		dagger.WithLogOutput(os.Stdout),
	)
	require.NoError(t, err)
	defer c.Close()

	randomID := identity.NewID()

	cache := c.CacheVolume("test-platform-cache-mount")

	// make sure cache mounts are inherently platform-agnostic
	cmds := make([]string, 0, len(platformToUname))
	for platform := range platformToUname {
		exit, err := c.Container(dagger.ContainerOpts{Platform: platform}).
			From("alpine:3.16").
			WithMountedCache("/cache", cache).
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
		WithMountedCache("/cache", cache).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"sh", "-x", "-c", strings.Join(cmds, " && ")},
		}).
		ExitCode(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, exit)
}

func TestPlatformInvalid(t *testing.T) {
	t.Parallel()

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

func TestPlatformWindows(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := dagger.Connect(ctx,
		dagger.WithLogOutput(os.Stdout),
	)
	require.NoError(t, err)
	defer c.Close()

	// It's not possible to exec, but we can pull and read files
	ents, err := c.Container(dagger.ContainerOpts{Platform: "windows/amd64"}).
		From("mcr.microsoft.com/windows/nanoserver:ltsc2022").
		Rootfs().
		Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"License.txt", "ProgramData", "Users", "Windows"}, ents)
}
