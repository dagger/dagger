package build

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/engine/distconsts"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/ci/consts"
	"github.com/dagger/dagger/ci/internal/dagger"
)

type sdkContent struct {
	index   ocispecs.Index
	sdkDir  *dagger.Directory
	envName string
}

func (content *sdkContent) apply(ctr *dagger.Container) *dagger.Container {
	manifest := content.index.Manifests[0]
	manifestDgst := manifest.Digest.String()

	return ctr.
		WithEnvVariable(content.envName, manifestDgst).
		WithDirectory(distconsts.EngineContainerBuiltinContentDir, content.sdkDir, dagger.ContainerWithDirectoryOpts{
			Include: []string{"blobs/"},
		})
}

type sdkContentF func(ctx context.Context) (*sdkContent, error)

func (build *Builder) pythonSDKContent(ctx context.Context) (*sdkContent, error) {
	rootfs := dag.Directory().WithDirectory("/", build.source.Directory("sdk/python"), dagger.DirectoryWithDirectoryOpts{
		Include: []string{
			"pyproject.toml",
			"src/**/*.py",
			"src/**/*.typed",
			"codegen/src/**/*.py",
			"codegen/pyproject.toml",
			"codegen/requirements.lock",
			"runtime/",
			"LICENSE",
			"README.md",
		},
	})

	sdkCtrTarball := dag.Container().
		WithRootfs(rootfs).
		AsTarball(dagger.ContainerAsTarballOpts{
			ForcedCompression: dagger.Uncompressed,
		})

	sdkDir := dag.Container().
		From(consts.AlpineImage).
		WithMountedDirectory("/out", dag.Directory()).
		WithMountedFile("/sdk.tar", sdkCtrTarball).
		WithExec([]string{"tar", "xf", "/sdk.tar", "-C", "/out"}).
		Directory("/out")

	var index ocispecs.Index
	indexContents, err := sdkDir.File("index.json").Contents(ctx)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(indexContents), &index); err != nil {
		return nil, err
	}

	return &sdkContent{
		index:   index,
		sdkDir:  sdkDir,
		envName: distconsts.PythonSDKManifestDigestEnvName,
	}, nil
}

func (build *Builder) typescriptSDKContent(ctx context.Context) (*sdkContent, error) {
	rootfs := dag.Directory().WithDirectory("/", build.source.Directory("sdk/typescript"), dagger.DirectoryWithDirectoryOpts{
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
		WithFile("/codegen", build.CodegenBinary()).
		AsTarball(dagger.ContainerAsTarballOpts{
			ForcedCompression: dagger.Uncompressed,
		})

	sdkDir := dag.Container().From("alpine:"+consts.AlpineVersion).
		WithMountedDirectory("/out", dag.Directory()).
		WithMountedFile("/sdk.tar", sdkCtrTarball).
		WithExec([]string{"tar", "xf", "/sdk.tar", "-C", "/out"}).
		Directory("/out")

	var index ocispecs.Index
	indexContents, err := sdkDir.File("index.json").Contents(ctx)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(indexContents), &index); err != nil {
		return nil, err
	}

	return &sdkContent{
		index:   index,
		sdkDir:  sdkDir,
		envName: distconsts.TypescriptSDKManifestDigestEnvName,
	}, nil
}

func (build *Builder) goSDKContent(ctx context.Context) (*sdkContent, error) {
	base := dag.Container(dagger.ContainerOpts{Platform: build.platform}).
		From(fmt.Sprintf("golang:%s-alpine%s", consts.GolangVersion, consts.AlpineVersion)).
		WithExec([]string{"apk", "add", "git"})

	sdkCtrTarball := base.
		WithEnvVariable("GOTOOLCHAIN", "auto").
		WithFile("/usr/local/bin/codegen", build.CodegenBinary()).
		WithEntrypoint([]string{"/usr/local/bin/codegen"}).
		AsTarball(dagger.ContainerAsTarballOpts{
			ForcedCompression: dagger.Uncompressed,
		})

	sdkDir := base.
		WithMountedDirectory("/out", dag.Directory()).
		WithMountedFile("/sdk.tar", sdkCtrTarball).
		WithExec([]string{"tar", "xf", "/sdk.tar", "-C", "/out"}).
		Directory("/out")

	var index ocispecs.Index
	indexContents, err := sdkDir.File("index.json").Contents(ctx)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(indexContents), &index); err != nil {
		return nil, err
	}

	return &sdkContent{
		index:   index,
		sdkDir:  sdkDir,
		envName: distconsts.GoSDKManifestDigestEnvName,
	}, nil
}
