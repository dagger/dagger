package build

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/engine/distconsts"

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

const NodeImage = "node:22.11.0-alpine"

func (build *Builder) typescriptSDKContent(ctx context.Context) (*sdkContent, error) {
	tsxNodeModule := dag.Container().
		From(NodeImage).
		WithExec([]string{"npm", "install", "-g", "tsx@4.15.6"}). // TODO: dont hardcode this version
		Directory("/usr/local/lib/node_modules/tsx")

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

	// sdkNodeModules, err := dag.Container().
	// 	From("oven/bun:1.1.38").
	// 	WithDirectory("/sdk", rootfs).
	// 	WithoutEntrypoint().
	// 	WithMountedCache("/root/.bun/install/cache", dag.CacheVolume(fmt.Sprintf("mod-bun-cache-%s", "1.1.8")), dagger.ContainerWithMountedCacheOpts{
	// 		Sharing: dagger.CacheSharingModePrivate,
	// 	}).
	// 	// WithExec([]string{"bun", "/opt/module/bin/__tsconfig.updator.ts"}).
	// 	WithExec([]string{"bun", "install", "--no-verify", "--no-progress", "--summary", "--shamefully-hoist", "/sdk"}).
	// 	Terminal().Sync(ctx)
	// if err != nil {
	// 	return nil, err
	// }

	sdkNodeModules := dag.Container().
		From(NodeImage).
		WithDirectory("./sdk", rootfs).
		WithoutEntrypoint().
		WithMountedCache("/root/.cache/yarn", dag.CacheVolume(fmt.Sprintf("yarn-cache-%s-%s", "node", "22.11.10"))).
		WithFile("./package.json", rootfs.File("./runtime/template/package.json")).
		WithExec([]string{"corepack", "enable"}).
		WithExec([]string{"corepack", "use", fmt.Sprintf("yarn@%s", "1.22.22+sha512.a6b2f7906b721bba3d67d4aff083df04dad64c399707841b7acf00f6b133b7ac24255f2652fa22ae3534329dc6180534e98d17432037ff6fd140556e2bb3137e")}).
		WithExec([]string{"yarn", "install", "--mode", "update-lockfile"}). // TODO: is update-lockfile necessary?
		Directory("/node_modules")

	sdkCtrTarball := dag.Container().
		WithRootfs(rootfs).
		WithFile("/codegen", build.CodegenBinary()).
		WithDirectory("/tsx_module", tsxNodeModule).
		WithDirectory("/sdk_node_modules", sdkNodeModules).
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
