package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
)

type PlatformSuite struct{}

func TestPlatform(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(PlatformSuite{})
}

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

func (PlatformSuite) TestEmulatedExecAndPush(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	variants := make([]*dagger.Container, 0, len(platformToUname))
	for platform, uname := range platformToUname {
		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(alpineImage).
			WithExec([]string{"uname", "-m"})
		variants = append(variants, ctr)

		ctrPlatform, err := ctr.Platform(ctx)
		require.NoError(t, err)
		require.Equal(t, platform, ctrPlatform)

		output, err := ctr.Stdout(ctx)
		require.NoError(t, err)
		output = strings.TrimSpace(output)
		require.Equal(t, uname, output)
	}

	testRef := registryRef("platform-emulated-exec-and-push")
	_, err := c.Container().Publish(ctx, testRef, dagger.ContainerPublishOpts{
		PlatformVariants: variants,
	})
	require.NoError(t, err)

	for platform, uname := range platformToUname {
		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(testRef).
			WithExec([]string{"uname", "-m"})
		output, err := ctr.Stdout(ctx)
		require.NoError(t, err)
		output = strings.TrimSpace(output)
		require.Equal(t, uname, output)
	}
}

func (PlatformSuite) TestCrossCompile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t, dagger.WithWorkdir("../.."))

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
				WithExec([]string{"sh", "-c", "uname -m && goxx-go build -o /out/dagger /src/cmd/dagger"})

			// using require in a goroutine brings down the whole test suite, so
			// use assert instead and return an error
			assertErr := fmt.Errorf("assertion failed for %s", platform)

			// should be running as the default (buildkit host) platform
			ctrPlatform, err := ctr.Platform(ctx)
			if !assert.NoError(t, err) {
				return assertErr
			}
			if !assert.Equal(t, defaultPlatform, ctrPlatform) {
				return assertErr
			}

			stdout, err := ctr.Stdout(ctx)
			if !assert.NoError(t, err) {
				return assertErr
			}
			stdout = strings.TrimSpace(stdout)
			if !assert.Equal(t, platformToUname[defaultPlatform], stdout) {
				return assertErr
			}

			out := ctr.Directory("/out")
			variants[i] = c.Container(dagger.ContainerOpts{Platform: platform}).WithRootfs(out)
			return nil
		})
	}
	require.NoError(t, eg.Wait())

	// make sure the binaries for each platform are executable via emulation now
	for _, ctr := range variants {
		_, err := ctr.
			WithExec([]string{"/dagger", "version"}).
			Sync(ctx)
		require.NoError(t, err)
	}

	// push a multiplatform image
	testRef := registryRef("platform-cross-compile")
	_, err = c.Container().Publish(ctx, testRef, dagger.ContainerPublishOpts{
		PlatformVariants: variants,
	})
	require.NoError(t, err)

	// pull the images, mount them all into a container and ensure the binaries are the right platform
	ctr := c.Container().From(alpineImage).WithExec([]string{"apk", "add", "file"})

	cmds := make([]string, 0, len(platformToFileArch))
	for platform, uname := range platformToFileArch {
		pulledDir := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(testRef).
			Rootfs()
		ctr = ctr.WithMountedDirectory("/"+string(platform), pulledDir)
		cmds = append(cmds, fmt.Sprintf(`file /%s/dagger | tee /dev/stderr | grep -q '%s'`, platform, uname))
	}
	_, err = ctr.
		WithExec([]string{"sh", "-x", "-e", "-c", strings.Join(cmds, "\n")}).
		Sync(ctx)
	require.NoError(t, err)
}

func (PlatformSuite) TestCacheMounts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	randomID := identity.NewID()

	cache := c.CacheVolume("test-platform-cache-mount")

	saveCacheMount := preventCacheMountPrune(c, t, cache)

	// make sure cache mounts are inherently platform-agnostic
	cmds := make([]string, 0, len(platformToUname))
	for platform := range platformToUname {
		_, err := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(alpineImage).
			With(saveCacheMount).
			WithMountedCache("/cache", cache).
			WithExec([]string{"sh", "-x", "-c", strings.Join([]string{
				"mkdir -p /cache/" + randomID + string(platform),
				"uname -m > /cache/" + randomID + string(platform) + "/uname",
			}, " && ")}).
			Sync(ctx)
		require.NoError(t, err)
		cmds = append(cmds, fmt.Sprintf(`cat /cache/%s%s/uname | grep '%s'`, randomID, platform, platformToUname[platform]))
	}

	_, err := c.Container().
		From(alpineImage).
		With(saveCacheMount).
		WithMountedCache("/cache", cache).
		WithExec([]string{"sh", "-x", "-c", strings.Join(cmds, " && ")}).
		Sync(ctx)
	require.NoError(t, err)
}

func (PlatformSuite) TestInvalid(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := c.Container(dagger.ContainerOpts{Platform: "windows98"}).ID(ctx)
	requireErrOut(t, err, "unknown operating system or architecture")
}

func (PlatformSuite) TestWindows(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// It's not possible to exec, but we can pull and read files
	ents, err := c.Container(dagger.ContainerOpts{Platform: "windows/amd64"}).
		From("mcr.microsoft.com/windows/nanoserver:ltsc2022").
		Rootfs().
		Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"License.txt", "ProgramData/", "Users/", "Windows/"}, ents)
}
