package build

import (
	"context"
	"encoding/json"
	"runtime"

	"github.com/dagger/dagger/engine/distconsts"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/.dagger/consts"
	"github.com/dagger/dagger/.dagger/internal/dagger"
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
			"uv.lock",
			"src/**/*.py",
			"src/**/*.typed",
			"codegen/",
			"runtime/",
			"LICENSE",
			"README.md",
		},
	})

	sdkCtrTarball := dag.Container().
		WithRootfs(rootfs).
		AsTarball(dagger.ContainerAsTarballOpts{
			ForcedCompression: dagger.ImageLayerCompressionZstd,
		})
	sdkDir := unpackTar(sdkCtrTarball)

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
			"src/**/*.ts",
			"LICENSE",
			"README.md",
			"runtime",
			"package.json",
			"dagger.json",
		},
		Exclude: []string{
			"src/**/test/*",
			"src/**/*.spec.ts",
			"src/provisioning",
		},
	})

	sdkCtrTarball := dag.Container().
		WithRootfs(rootfs).
		WithFile("/codegen", build.CodegenBinary()).
		AsTarball(dagger.ContainerAsTarballOpts{
			ForcedCompression: dagger.ImageLayerCompressionZstd,
		})
	sdkDir := unpackTar(sdkCtrTarball)

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
	sdkCache := dag.Container().
		From(consts.GolangImage).
		With(build.goPlatformEnv).
		// import xx
		WithDirectory("/", dag.Container().From(consts.XxImage).Rootfs()).
		// set envs read by xx
		WithEnvVariable("BUILDPLATFORM", "linux/"+runtime.GOARCH).
		WithEnvVariable("TARGETPLATFORM", string(build.platform)).
		// pre-cache stdlib
		WithExec([]string{"xx-go", "build", "std"}).
		// pre-cache common deps
		WithDirectory("/sdk", build.source.Directory("sdk/go")).
		WithExec([]string{"xx-go", "list",
			"-C", "/sdk",
			"-e",
			"-export=true",
			"-compiled=true",
			"-deps=true",
			"-test=false",
			".",
		})

	sdkCtrTarball := dag.Container(dagger.ContainerOpts{Platform: build.platform}).
		From(consts.GolangImage).
		With(build.goPlatformEnv).
		WithExec([]string{"apk", "add", "git"}).
		WithEnvVariable("GOTOOLCHAIN", "auto").
		WithFile("/usr/local/bin/codegen", build.CodegenBinary()).
		// these cache directories should match the cache volume locations in the engine's goSDK.base
		WithDirectory("/go/pkg/mod", sdkCache.Directory("/go/pkg/mod")).
		WithDirectory("/root/.cache/go-build", sdkCache.Directory("/root/.cache/go-build")).
		AsTarball(dagger.ContainerAsTarballOpts{
			ForcedCompression: dagger.ImageLayerCompressionZstd,
		})
	sdkDir := unpackTar(sdkCtrTarball)

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

func (build *Builder) javaSDKContent(ctx context.Context) (*sdkContent, error) {
	rootfs := dag.Directory().WithDirectory("/", build.source.Directory("sdk/java"), dagger.DirectoryWithDirectoryOpts{
		Include: []string{
			"dagger-codegen-maven-plugin",
			"dagger-java-annotation-processor",
			"dagger-java-sdk",
			"module.jar",
			"LICENSE",
			"README.md",
			"runtime",
			"pom.xml",
			"dagger.json",
		},
		Exclude: []string{
			"**/src/test/*",
			".mvn",
			"mvnw",
			"mvnw.cmd",
			"**/target/*",
		},
	})

	sdkCtrTarball := dag.Container().
		WithRootfs(rootfs).
		WithFile("/codegen", build.CodegenBinary()).
		AsTarball(dagger.ContainerAsTarballOpts{
			ForcedCompression: dagger.ImageLayerCompressionZstd,
		})
	sdkDir := unpackTar(sdkCtrTarball)

	var index ocispecs.Index
	indexContents, err := sdkDir.File("index.json").Contents(ctx)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(indexContents), &index); err != nil {
		return nil, err
	}

	return &sdkContent{
		index:   index,
		sdkDir:  sdkDir,
		envName: distconsts.JavaSDKManifestDigestEnvName,
	}, nil
}

func unpackTar(tarball *dagger.File) *dagger.Directory {
	return dag.
		Alpine(dagger.AlpineOpts{
			Branch: consts.AlpineVersion,
		}).
		Container().
		WithMountedDirectory("/out", dag.Directory()).
		WithMountedFile("/target.tar", tarball).
		WithExec([]string{"tar", "xf", "/target.tar", "-C", "/out"}).
		Directory("/out")
}
