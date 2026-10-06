package build

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"runtime"
	"strconv"
	"strings"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/mod/modfile"

	"github.com/dagger/dagger/engine/distconsts"

	"dagger/engine-dev/consts"
	"dagger/engine-dev/internal/dagger"

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
	pythonImageSource := build.source.Directory("sdk/python/runtime/images/base")
	uvImageSource := build.source.Directory("sdk/python/runtime/images/uv")

	pySrc := dag.Directory().WithDirectory(
		"/",
		build.source.Directory("sdk/python"),
		dagger.DirectoryWithDirectoryOpts{
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
		},
	)

	buildBase := pythonImageSource.DockerBuild(dagger.DirectoryDockerBuildOpts{
		Target: "base",
	})

	buildUV := uvImageSource.DockerBuild(dagger.DirectoryDockerBuildOpts{
		Target: "uv",
	})

	targetUV := uvImageSource.DockerBuild(dagger.DirectoryDockerBuildOpts{
		Platform: build.platform,
		Target:   "uv",
	})

	// bundle the codegen script and its dependencies into a single executable
	codegen := buildBase.
		WithWorkdir("/src").
		WithDirectory(
			"/usr/local/bin",
			buildUV.Rootfs(),
			dagger.ContainerWithDirectoryOpts{Include: []string{"uv*"}},
		).
		// mount the whole uv workspace so the export honors uv.lock instead of
		// resolving the latest versions of codegen's dependencies
		WithMountedDirectory("", pySrc).
		WithEnvVariable("UV_NATIVE_TLS", "true").
		WithExec([]string{
			"uv", "export",
			"--frozen",
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

	rootfs := pySrc.
		// bundle the uv binaries
		WithDirectory("dist", targetUV.Rootfs(), dagger.DirectoryWithDirectoryOpts{
			Include: []string{"uv*"},
		}).
		WithFile("dist/codegen", codegen)

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
		// Install from the committed yarn.lock (bun migrates it) instead of
		// resolving package.json ranges against whatever is newest on npm.
		// It's added to the builder only, so it isn't shipped in the SDK image.
		WithFile("/src/yarn.lock", build.source.File("sdk/typescript/yarn.lock")).
		WithWorkdir("/src").
		WithExec([]string{"bun", "install", "--frozen-lockfile"}).
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
	seedMod, libVersion, err := goModuleSeed(ctx, build.source)
	if err != nil {
		return nil, err
	}
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
		}).
		// pre-cache what module codegen downloads for a generated module (see
		// goModuleSeed): the pinned SDK version, everything `go mod tidy`
		// needs, and the version info `go get` reads
		WithDirectory("/seed", seedMod).
		WithWorkdir("/seed").
		WithExec([]string{"go", "get", "dagger.io/dagger@" + libVersion}).
		WithExec([]string{"go", "mod", "tidy"}).
		WithExec([]string{"go", "list", "-m", "all"})

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
		Wolfi().
		Container().
		WithMountedDirectory("/out", dag.Directory()).
		WithMountedFile("/target.tar", tarball).
		WithExec([]string{"tar", "xf", "/target.tar", "-C", "/out"}).
		Directory("/out")
}

// goModuleSeed returns a Go module shaped like the ones module codegen
// generates, and the dagger.io/dagger version codegen pins in them.
//
// The module has the Go SDK's requirements and the replace directives
// codegen copies with them, the SDK's go.sum, and the third-party imports
// of generated code (sdk/go/internal/codegendeps). Running codegen's
// commands in it fills the module cache with what codegen downloads for a
// generated module that has no dependencies of its own.
func goModuleSeed(ctx context.Context, source *dagger.Directory) (*dagger.Directory, string, error) {
	sdkGoMod, err := source.File("sdk/go/go.mod").Contents(ctx)
	if err != nil {
		return nil, "", err
	}
	sdkMod, err := modfile.Parse("go.mod", []byte(sdkGoMod), nil)
	if err != nil {
		return nil, "", fmt.Errorf("parse sdk/go/go.mod: %w", err)
	}
	seedMod := new(modfile.File)
	if err := seedMod.AddModuleStmt("seed"); err != nil {
		return nil, "", err
	}
	if err := seedMod.AddGoStmt(sdkMod.Go.Version); err != nil {
		return nil, "", err
	}
	required := map[string]bool{}
	for _, req := range sdkMod.Require {
		seedMod.AddNewRequire(req.Mod.Path, req.Mod.Version, req.Indirect)
		required[req.Mod.Path] = true
	}
	// like codegen, keep replace directives only for required modules
	for _, rep := range sdkMod.Replace {
		if required[rep.New.Path] {
			if err := seedMod.AddReplace(rep.Old.Path, rep.Old.Version, rep.New.Path, rep.New.Version); err != nil {
				return nil, "", err
			}
		}
	}
	goMod, err := seedMod.Format()
	if err != nil {
		return nil, "", err
	}

	imports, err := goFileImports(ctx, source.File("sdk/go/internal/codegendeps/codegendeps.go"))
	if err != nil {
		return nil, "", err
	}
	var mainGo strings.Builder
	mainGo.WriteString("package main\n\nimport (\n")
	for _, imp := range imports {
		fmt.Fprintf(&mainGo, "\t_ %q\n", imp)
	}
	mainGo.WriteString(")\n\nfunc main() {}\n")

	libVersion, err := goSDKLibVersion(ctx, source.File("core/sdk/go_sdk.go"))
	if err != nil {
		return nil, "", err
	}

	return dag.Directory().
		WithNewFile("go.mod", string(goMod)).
		WithFile("go.sum", source.File("sdk/go/go.sum")).
		WithNewFile("main.go", mainGo.String()), libVersion, nil
}

// goFileImports returns the import paths of a Go file.
func goFileImports(ctx context.Context, file *dagger.File) ([]string, error) {
	src, err := file.Contents(ctx)
	if err != nil {
		return nil, err
	}
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var imports []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		imports = append(imports, path)
	}
	return imports, nil
}

// goSDKLibVersion returns the dagger.io/dagger version the engine has module
// codegen pin: the goSDKLibVersion constant in core/sdk/go_sdk.go.
func goSDKLibVersion(ctx context.Context, file *dagger.File) (string, error) {
	src, err := file.Contents(ctx)
	if err != nil {
		return "", err
	}
	f, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		return "", err
	}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vspec := spec.(*ast.ValueSpec)
			for i, name := range vspec.Names {
				if name.Name != "goSDKLibVersion" || i >= len(vspec.Values) {
					continue
				}
				if lit, ok := vspec.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					return strconv.Unquote(lit.Value)
				}
			}
		}
	}
	return "", fmt.Errorf("goSDKLibVersion constant not found in core/sdk/go_sdk.go")
}
