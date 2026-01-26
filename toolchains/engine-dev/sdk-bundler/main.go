package main

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	"dagger/sdk-bundler/internal/dagger"

	"github.com/dagger/dagger/engine/distconsts"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func New(
	ctx context.Context,
	// +optional
	platform dagger.Platform,
) (*SdkBundler, error) {
	if platform == "" {
		var err error
		platform, err = dag.DefaultPlatform(ctx)
		if err != nil {
			return nil, err
		}
	}
	return &SdkBundler{Platform: platform}, nil
}

type SdkBundler struct {
	Platform dagger.Platform
}

type sdkContent struct {
	Index   ocispecs.Index // +private
	SdkDir  *dagger.Directory
	EnvName string
}

func (content *sdkContent) Apply(ctr *dagger.Container) *dagger.Container {
	manifest := content.Index.Manifests[0]
	manifestDgst := manifest.Digest.String()

	return ctr.
		WithEnvVariable(content.EnvName, manifestDgst).
		WithDirectory(distconsts.EngineContainerBuiltinContentDir, content.SdkDir, dagger.ContainerWithDirectoryOpts{
			Include: []string{"blobs/"},
		})
}

func (bundler *SdkBundler) Python(
	ctx context.Context,
	// +defaultPath="/sdk/python/runtime/images/base/Dockerfile"
	baseDockerfile *dagger.File,
	// +defaultPath="/sdk/python/runtime/images/uv/Dockerfile"
	uvDockerfile *dagger.File,
	// +defaultPath="/sdk/python"
	// +ignore=[
	// 	"src/dagger/_engine/",
	//  "src/dagger/provisioning/",
	//  "!pyproject.toml",
	//  "!uv.lock",
	//  "!src/**/*.py",
	//  "!src/**/*.typed",
	//  "!codegen/",
	//  "!runtime/",
	//  "!LICENSE",
	//  "!README.md"
	// ]
	sdkSource *dagger.Directory,
) (*sdkContent, error) {
	// FIXME: why use Dockerfiles???
	base := dag.Directory().
		WithFile("Dockerfile", baseDockerfile).
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Platform: bundler.Platform,
			Target:   "base",
		})
	uv := dag.Directory().
		WithFile("Dockerfile", uvDockerfile).
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Platform: bundler.Platform,
			Target:   "uv",
		})
	uvBinaries := uv.Rootfs().Filter(dagger.DirectoryFilterOpts{Include: []string{"uv*"}})
	codegenBinary := base.
		WithWorkdir("/src").
		WithDirectory("/usr/local/bin", uvBinaries).
		WithMountedDirectory("", sdkSource.Directory("codegen")).
		WithEnvVariable("UV_NATIVE_TLS", "true").
		WithExec([]string{
			"uv", "export",
			"--no-hashes",
			"--no-editable",
			"--package", "codegen",
			"-o", "/requirements.txt",
		}).
		WithExec([]string{
			"uvx", "shiv==1.0.8", // this version doesn't need to be constantly updated
			"--reproducible",
			"--compressed",
			"-e", "codegen.cli:main",
			"-o", "/codegen",
			"-r", "/requirements.txt",
		}).
		File("/codegen")

	rootfs := sdkSource.
		WithDirectory("dist", uvBinaries).
		// bundle the codegen script and its dependencies into a single executable
		WithFile("dist/codegen", codegenBinary)

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
		Index:   index,
		SdkDir:  sdkDir,
		EnvName: distconsts.PythonSDKManifestDigestEnvName,
	}, nil
}

const TypescriptSDKTSXVersion = "4.15.6"

func (build *Builder) typescriptSDKContent(ctx context.Context) (*sdkContent, error) {
	tsxNodeModule := dag.Container(dagger.ContainerOpts{Platform: build.platform}).
		From(tsdistconsts.DefaultNodeImageRef).
		WithExec([]string{"npm", "install", "-g", fmt.Sprintf("tsx@%s", TypescriptSDKTSXVersion)}).
		Directory("/usr/local/lib/node_modules/tsx")

	rootfs := dag.Directory().WithDirectory("/", build.source.Directory("sdk/typescript"), dagger.DirectoryWithDirectoryOpts{
		Include: []string{
			"src/**/*.ts",
			"LICENSE",
			"README.md",
			"runtime",
			"package.json",
			"tsconfig.json",
			"rollup.dts.config.mjs",
			"dagger.json",
		},
		Exclude: []string{
			"src/**/test/*",
			"src/**/*.spec.ts",
		},
	})

	bunBuilderCtr := dag.Container(dagger.ContainerOpts{Platform: build.platform}).
		From(tsdistconsts.DefaultBunImageRef).
		// NodeJS is required to run tsc.
		WithExec([]string{"apk", "add", "nodejs"}).
		// Install tsc binary.
		WithExec([]string{"bun", "install", "-g", "typescript"}).
		// We cannot mount the directory because bun will struggle with symlinks when compiling
		// the introspector binary.
		WithDirectory("/src", rootfs).
		WithWorkdir("/src").
		WithExec([]string{"bun", "install"}).
		// Create introspector binary
		WithExec([]string{"bun", "build", "src/module/entrypoint/introspection_entrypoint.ts", "--compile", "--outfile", "/bin/ts-introspector"}).
		// Build the SDK bundled that contains the whole static library + default client
		// The bundle works for all runtimes as long as we target node since deno & bun have compatibility API for node.
		WithExec([]string{"bun", "build", "./src/index.ts", "--external=typescript", "--target=node", "--outfile", "/out-node/core.js"}).
		// Emit type declaration for these files
		WithExec([]string{"tsc", "--emitDeclarationOnly"}).
		WithExec([]string{"bun", "x", "rollup", "-c", "rollup.dts.config.mjs", "-o", "/out-node/core.d.ts"})

	sdkCtrTarball := dag.Container().
		WithRootfs(rootfs).
		WithFile("/codegen", build.CodegenBinary()).
		// We need to mount the typescript library because bun will not be able to resolve the
		// typescript library when introspecting the user's module.
		// TODO: As a follow up, this also enable skipping dependencies installation inside the module
		// runtime if only typescript library is used (by default)
		WithDirectory("/typescript-library", bunBuilderCtr.Directory("/src/node_modules/typescript")).
		WithFile("/bin/ts-introspector", bunBuilderCtr.File("/bin/ts-introspector")).
		WithDirectory("/tsx_module", tsxNodeModule).
		WithDirectory("/bundled_lib", bunBuilderCtr.Directory("/out-node")).
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
		Index:   index,
		SdkDir:  sdkDir,
		EnvName: distconsts.TypescriptSDKManifestDigestEnvName,
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
		WithExec([]string{
			"xx-go", "list",
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
		WithExec([]string{"apk", "add", "git", "openssh", "openssl"}).
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
		Index:   index,
		SdkDir:  sdkDir,
		EnvName: distconsts.GoSDKManifestDigestEnvName,
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
