package util

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/engine/distconsts"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	. "dagger/internal/dagger"
)

func (repo *Repository) pythonSDKContent(ctx context.Context, arch string) WithContainerFunc {
	return func(ctr *Container) *Container {
		rootfs := dag.Directory().WithDirectory("/", repo.Directory.Directory("sdk/python"), DirectoryWithDirectoryOpts{
			Include: []string{
				"pyproject.toml",
				"src/**/*.py",
				"src/**/*.typed",
				"runtime/",
				"LICENSE",
				"README.md",
			},
		})

		sdkCtrTarball := dag.Container().
			WithRootfs(rootfs).
			WithFile("/codegen", repo.codegenBinary(arch, "")).
			AsTarball(ContainerAsTarballOpts{
				ForcedCompression: Uncompressed,
			})

		sdkDir := dag.Container().From("alpine:"+alpineVersion).
			WithMountedDirectory("/out", dag.Directory()).
			WithMountedFile("/sdk.tar", sdkCtrTarball).
			WithExec([]string{"tar", "xf", "/sdk.tar", "-C", "/out"}).
			Directory("/out")

		content, err := sdkContent(ctx, ctr, sdkDir, distconsts.PythonSDKManifestDigestEnvName)
		if err != nil {
			// FIXME: would be nice to not panic
			panic(err)
		}
		return content
	}
}

func (repo *Repository) typescriptSDKContent(ctx context.Context, arch string) WithContainerFunc {
	return func(ctr *Container) *Container {
		rootfs := dag.Directory().WithDirectory("/", repo.Directory.Directory("sdk/typescript"), DirectoryWithDirectoryOpts{
			Include: []string{
				"**/*.ts",
				"LICENSE",
				"README.md",
				"runtime",
				"package.json",
				"dagger.json",
			},
			Exclude: []string{
				"node_modules",
				"dist",
				"**/test",
				"**/*.spec.ts",
				"dev",
			},
		})
		sdkCtrTarball := dag.Container().
			WithRootfs(rootfs).
			WithFile("/codegen", repo.codegenBinary(arch, "")).
			AsTarball(ContainerAsTarballOpts{
				ForcedCompression: Uncompressed,
			})

		sdkDir := dag.Container().From("alpine:"+alpineVersion).
			WithMountedDirectory("/out", dag.Directory()).
			WithMountedFile("/sdk.tar", sdkCtrTarball).
			WithExec([]string{"tar", "xf", "/sdk.tar", "-C", "/out"}).
			Directory("/out")

		content, err := sdkContent(ctx, ctr, sdkDir, distconsts.TypescriptSDKManifestDigestEnvName)
		if err != nil {
			// FIXME: would be nice to not panic
			panic(err)
		}
		return content
	}
}

func (repo *Repository) goSDKContent(ctx context.Context, arch string) WithContainerFunc {
	return func(ctr *Container) *Container {
		base := dag.Container(ContainerOpts{Platform: Platform("linux/" + arch)}).
			From(fmt.Sprintf("golang:%s-alpine%s", golangVersion, alpineVersion))

		sdkCtrTarball := base.
			WithEnvVariable("GOTOOLCHAIN", "auto").
			WithFile("/usr/local/bin/codegen", repo.codegenBinary(arch, "")).
			WithEntrypoint([]string{"/usr/local/bin/codegen"}).
			AsTarball(ContainerAsTarballOpts{
				ForcedCompression: Uncompressed,
			})

		sdkDir := base.
			WithMountedDirectory("/out", dag.Directory()).
			WithMountedFile("/sdk.tar", sdkCtrTarball).
			WithExec([]string{"tar", "xf", "/sdk.tar", "-C", "/out"}).
			Directory("/out")

		content, err := sdkContent(ctx, ctr, sdkDir, distconsts.GoSDKManifestDigestEnvName)
		if err != nil {
			// FIXME: would be nice to not panic
			panic(err)
		}
		return content
	}
}

func sdkContent(ctx context.Context, ctr *Container, sdkDir *Directory, envName string) (*Container, error) {
	var index ocispecs.Index
	indexContents, err := sdkDir.File("index.json").Contents(ctx)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(indexContents), &index); err != nil {
		return nil, err
	}
	manifest := index.Manifests[0]
	manifestDgst := manifest.Digest.String()

	return ctr.
		WithEnvVariable(envName, manifestDgst).
		WithDirectory(distconsts.EngineContainerBuiltinContentDir, sdkDir, ContainerWithDirectoryOpts{
			Include: []string{"blobs/"},
		}), nil
}
