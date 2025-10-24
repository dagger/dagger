package build

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/engine/distconsts"

	"github.com/dagger/dagger/cmd/engine/.dagger/consts"
	"github.com/dagger/dagger/cmd/engine/.dagger/internal/dagger"
	"github.com/dagger/dagger/sdk/typescript/runtime/tsdistconsts"
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
	docker := dag.Directory().WithFile("", build.source.File("sdk/python/runtime/Dockerfile"))

	base := docker.DockerBuild(dagger.DirectoryDockerBuildOpts{
		Platform: build.platform,
		Target:   "base",
	})

	uv := docker.DockerBuild(dagger.DirectoryDockerBuildOpts{
		Platform: build.platform,
		Target:   "uv",
	})

	rootfs := dag.Directory().
		WithDirectory("/", build.source.Directory("sdk/python"), dagger.DirectoryWithDirectoryOpts{
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
			// These components are not needed in modules
			Exclude: []string{
				"src/dagger/_engine/",
				"src/dagger/provisioning/",
			},
		}).
		// bundle the uv binaries
		WithDirectory("dist", uv.Rootfs(), dagger.DirectoryWithDirectoryOpts{
			Include: []string{"uv*"},
		})

	rootfs = rootfs.
		// bundle the codegen script and its dependencies into a single executable
		WithFile("dist/codegen", base.
			WithWorkdir("/src").
			WithDirectory("/usr/local/bin", rootfs.Directory("dist")).
			WithMountedDirectory("", rootfs.Directory("codegen")).
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
			File("/codegen"),
		)

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
		index:   index,
		sdkDir:  sdkDir,
		envName: distconsts.GoSDKManifestDigestEnvName,
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
