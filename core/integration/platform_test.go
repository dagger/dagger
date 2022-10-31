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
	variants := make([]dagger.ContainerID, len(platformToFileArch))
	i := 0
	var eg errgroup.Group
	for platform := range platformToUname {
		i++
		i := i - 1
		platform := platform
		eg.Go(func() error {
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
			variants[i] = id
			return nil
		})
	}
	require.NoError(t, eg.Wait())

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
		FS().
		Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"License.txt", "ProgramData", "Users", "Windows"}, ents)
}

func TestPlatformWasm(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := dagger.Connect(ctx,
		dagger.WithLogOutput(os.Stdout),
	)
	require.NoError(t, err)
	defer c.Close()

	// rust code that will be compiled to wasm
	helloRust, err := c.Directory().WithNewFile("hello.rs", dagger.DirectoryWithNewFileOpts{
		Contents: `
use std::fs;
use std::env;
fn main() { 
	let paths = fs::read_dir("/mnt").unwrap();
	for path in paths {
		println!("File: {}", path.unwrap().path().display())
	}

	let testenv = env::var("TEST_ENV").unwrap();
	println!("Env: {}", testenv);
}
`,
	}).ID(ctx)
	require.NoError(t, err)

	// compiled wasm binary
	helloWasm, err := c.Container().
		From("rust:1-alpine").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"rustup", "target", "add", "wasm32-wasi"},
		}).
		WithMountedDirectory("/src", helloRust).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"rustc", "/src/hello.rs", "--target=wasm32-wasi"},
		}).
		File("hello.wasm").
		ID(ctx)
	require.NoError(t, err)

	// directory we'll mount in to verify we can include dirs in the wasm sandbox
	testDir, err := c.Directory().
		WithNewFile("hello.txt", dagger.DirectoryWithNewFileOpts{
			Contents: "IMINWASM",
		}).
		ID(ctx)
	require.NoError(t, err)

	// run the wasm binary by specifying the wasm platform
	ctr := c.Container(dagger.ContainerOpts{Platform: "wasi/wasm32"}).
		WithMountedFile("/hello.wasm", helloWasm).
		WithMountedDirectory("/mnt", testDir).     // verify mounts show up in wasi sandbox too
		WithEnvVariable("TEST_ENV", "IM IN WASM"). // verify env vars show up
		Exec(dagger.ContainerExecOpts{
			Args: []string{"/hello.wasm"},
		})

	output, err := ctr.Stdout().Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "File: /mnt/hello.txt\nEnv: IM IN WASM\n", output)

	/* Test that you can pull a wasm image and run it
	NOTE: https://hub.docker.com/r/michaelirwin244/wasm-example doesn't work, error is:

	incompatible import type for `wasi_snapshot_preview1::sock_accept`
	function types incompatible: expected func of type `(i32, i32) -> (i32)`, found func of type `(i32, i32 , i32) -> (i32)`

	Seems to be an ABI incompatibility issue basically: https://github.com/second-state/wasmedge_wasi_socket/issues/28
	*/
	output, err = c.Container(dagger.ContainerOpts{Platform: "wasi/wasm32"}).
		From("eriksipsma/wasm-example:latest"). // TODO: don't merge test w/ this dep on my dockerhub registry
		Exec().
		Stdout().Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "Hello from WASM!\n", output)
}

/*
		helloRust, err = c.Directory().WithNewFile("hello.rs", dagger.DirectoryWithNewFileOpts{
			Contents: `
	fn main() {
		println!("Hello from WASM!");
	}
	`,
		}).ID(ctx)
		require.NoError(t, err)

		// compiled wasm binary
		helloWasm, err = c.Container().
			From("rust:1-alpine").
			Exec(dagger.ContainerExecOpts{
				Args: []string{"rustup", "target", "add", "wasm32-wasi"},
			}).
			WithMountedDirectory("/src", helloRust).
			Exec(dagger.ContainerExecOpts{
				Args: []string{"rustc", "/src/hello.rs", "--target=wasm32-wasi"},
			}).
			File("hello.wasm").
			ID(ctx)
		require.NoError(t, err)

		helloWasmDir, err := c.Directory().
			WithCopiedFile("/hello.wasm", helloWasm).
			ID(ctx)
		require.NoError(t, err)

		_, err = c.Container(dagger.ContainerOpts{Platform: "wasi/wasm32"}).
			WithFS(helloWasmDir).
			WithEntrypoint([]string{"/hello.wasm"}).
			Publish(ctx, "eriksipsma/wasm-example:latest")
		require.NoError(t, err)
*/
