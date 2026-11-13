package build

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"dagger.io/dagger/core"
	toml "github.com/pelletier/go-toml"
	"golang.org/x/mod/modfile"

	"github.com/dagger/dagger/engine/distconsts"

	"dagger/engine-dev/consts"
	"dagger/engine-dev/internal/dagger"
	golang "dagger/engine-dev/internal/dagger/clients/go"

	"github.com/dagger/dagger/sdk/typescript/runtime/tsdistconsts"
)

// SDKFiles are a built-in SDK's files. The engine image's entries ship them as
// a directory, and the engine finds it by its content digest, which the
// image gives in the variable EnvName.
type SDKFiles struct {
	// Name names the SDK: go, python or typescript.
	Name    string
	EnvName string
	Dir     *dagger.Directory
}

// SDKFiles are the built-in SDKs' files, for the image of build's platform.
func (build *Builder) SDKFiles(ctx context.Context) ([]SDKFiles, error) {
	goFiles, err := build.goSDKFiles(ctx)
	if err != nil {
		return nil, err
	}
	return []SDKFiles{
		{Name: "go", EnvName: distconsts.GoSDKDigestEnvName, Dir: goFiles},
		{Name: "python", EnvName: distconsts.PythonSDKDigestEnvName, Dir: build.pythonSDKFiles()},
		{Name: "typescript", EnvName: distconsts.TypescriptSDKDigestEnvName, Dir: build.typescriptSDKFiles()},
	}, nil
}

func (build *Builder) pythonSDKFiles() *dagger.Directory {
	pythonImageSource := build.source.Directory("sdk/python/runtime/images/base")
	uvImageSource := build.source.Directory("sdk/python/runtime/images/uv")

	pySrc := core.NewDirectory().WithDirectory(
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

	return pySrc.
		// bundle the uv binaries
		WithDirectory("dist", targetUV.Rootfs(), dagger.DirectoryWithDirectoryOpts{
			Include: []string{"uv*"},
		}).
		WithFile("dist/codegen", codegen)
}

const TypescriptSDKTSXVersion = "4.15.6"

func (build *Builder) typescriptSDKFiles() *dagger.Directory {
	tsxNodeModule := core.NewContainer(dagger.ContainerOpts{Platform: build.platform}).
		From(tsdistconsts.DefaultNodeImageRef).
		WithExec([]string{"npm", "install", "-g", fmt.Sprintf("tsx@%s", TypescriptSDKTSXVersion)}).
		Directory("/usr/local/lib/node_modules/tsx")

	rootfs := core.NewDirectory().WithDirectory("/", build.source.Directory("sdk/typescript"), dagger.DirectoryWithDirectoryOpts{
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

	bunBuilderCtr := core.NewContainer(dagger.ContainerOpts{Platform: build.platform}).
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

	return rootfs.
		WithFile("codegen", build.CodegenBinary()).
		// We need to mount the typescript library because bun will not be able to resolve the
		// typescript library when introspecting the user's module.
		// TODO: As a follow up, this also enable skipping dependencies installation inside the module
		// runtime if only typescript library is used (by default)
		WithDirectory("typescript-library", bunBuilderCtr.Directory("/src/node_modules/typescript")).
		WithFile("bin/ts-introspector", bunBuilderCtr.File("/bin/ts-introspector")).
		WithDirectory("tsx_module", tsxNodeModule).
		WithDirectory("bundled_lib", bunBuilderCtr.Directory("/out-node"))
}

// ExportDriver is golangci-lint's export driver: a GOPACKAGESDRIVER that
// serves a program loading Go packages, such as codegen, the export data of
// what they import from dagger/go's archives, so the go command compiles none
// of it.
func (build *Builder) ExportDriver() *dagger.File {
	return goBinary(core.Git("https://github.com/dagger/golangci-lint").
		Commit(consts.GolangciLintCommit).
		Tree().
		Directory("helpers/export-driver"), ".", build.platformSpec, golang.GoOpts{
		Version: consts.GolangVersion,
	})
}

// goSDKFiles are the built-in Go SDK's files: the codegen binary and the
// export driver in usr/local/bin, the SDK's Dang module in sdk/runtime, the
// module cache codegen starts from in go/pkg/mod, and in apk what installing
// git and SSH adds to the Go toolchain image. The engine puts them on that
// image (core/sdk's goSDKContainer).
func (build *Builder) goSDKFiles(ctx context.Context) (*dagger.Directory, error) {
	seedMod, libVersion, err := goModuleSeed(ctx, build.source)
	if err != nil {
		return nil, err
	}
	sdkCache := core.NewContainer().
		From(consts.GolangImage).
		// pre-cache what module codegen downloads for a generated module (see
		// goModuleSeed): the pinned SDK version, everything `go mod tidy`
		// needs, and the version info `go get` reads
		WithDirectory("/seed", seedMod).
		WithWorkdir("/seed").
		WithExec([]string{"go", "get", "dagger.io/dagger@" + libVersion}).
		WithExec([]string{"go", "mod", "tidy"}).
		WithExec([]string{"go", "list", "-m", "all"})

	// golangci-lint's export driver serves codegen the export data of what a
	// module imports from dagger/go's archives, so no Go build cache ships.
	exportDriver := build.ExportDriver()

	// Installed here, for the image's platform, so no engine installs them:
	// the engine copies in what the install added.
	toolchain := core.NewContainer(dagger.ContainerOpts{Platform: build.platform}).From(distconsts.GoSDKGolangImage)
	withGitAndSSH := toolchain.WithExec(append([]string{"apk", "add"}, distconsts.GoSDKBasePackages...))

	return core.NewDirectory().
		// apk's log records when the install ran, so it would make these
		// files different on every build.
		WithDirectory("apk", toolchain.Rootfs().Diff(withGitAndSSH.Rootfs()).WithoutFile("var/log/apk.log")).
		WithFile("usr/local/bin/codegen", build.CodegenBinary()).
		WithFile("usr/local/bin/export-driver", exportDriver).
		// The Go SDK module, which the engine loads for every module with the
		// Go runtime. It depends on dagger/go by go-sdk's pin, and builds
		// every runtime with it as dagger/go builds a user's Go code: one
		// graph of package builds and one cache.
		WithDirectory("sdk/runtime", build.source.Directory("sdk/go/runtime")).
		// this cache directory seeds the module cache volume the engine mounts at /go/pkg/mod (core/sdk's goSDKConstructorArgs).
		// Go's checksum database answers are left out: each records the
		// database's size and signature when it was fetched, so they would
		// make these files different on every build.
		WithDirectory("go/pkg/mod", sdkCache.Directory("/go/pkg/mod").WithoutDirectory("cache/download/sumdb")), nil
}

// GoImageEntriesSeed is the Go module whose dependencies' builds the engine
// image ships for Go module runtimes: a module as module codegen generates
// one (goModuleSeed), tidied with the dagger.io/dagger version codegen pins,
// whose main package imports every package of the standard library a build
// can import and everything generated module code imports. The builds of what its
// root package imports are what every Go module's codegen and runtime build
// read first: the standard library and the constant dependencies.
func (build *Builder) GoImageEntriesSeed(ctx context.Context) (*dagger.Directory, error) {
	seedMod, libVersion, err := goModuleSeed(ctx, build.source)
	if err != nil {
		return nil, err
	}
	stdList, err := core.NewContainer().
		From(consts.GolangImage).
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"go", "list", "-e", "-f", "{{if and (not .Incomplete) (ne .Name \"main\") (not .Error)}}{{.ImportPath}}{{end}}", "std"}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	generated, err := goFileImports(ctx, build.source.File("sdk/go/internal/codegendeps/codegendeps.go"))
	if err != nil {
		return nil, err
	}
	// Generated module code imports these, and none of dagger.io/dagger:
	// a module has bindings of its own.
	imports := generated
	for _, pkg := range strings.Split(strings.TrimSpace(stdList), "\n") {
		if pkg == "" || strings.HasPrefix(pkg, "vendor/") || strings.HasPrefix(pkg, "cmd/") ||
			pkg == "internal" || strings.HasPrefix(pkg, "internal/") || strings.Contains(pkg, "/internal/") || strings.HasSuffix(pkg, "/internal") {
			continue
		}
		imports = append(imports, pkg)
	}
	var mainGo strings.Builder
	mainGo.WriteString("package main\n\nimport (\n")
	for _, imp := range imports {
		fmt.Fprintf(&mainGo, "\t_ %q\n", imp)
	}
	mainGo.WriteString(")\n\nfunc main() {}\n")
	return core.NewContainer().
		From(consts.GolangImage).
		WithEnvVariable("GOPROXY", seedGoProxy).
		WithDirectory("/seed", seedMod.WithNewFile("main.go", mainGo.String())).
		WithWorkdir("/seed").
		WithExec([]string{"go", "get", "dagger.io/dagger@" + libVersion}).
		WithExec([]string{"go", "mod", "tidy"}).
		Directory("/seed"), nil
}

// seedGoProxy is the GOPROXY of the seeds' downloads: proxy.golang.org
// sometimes resets a module zip download mid-stream, which Go's default fails
// on; "|" retries the proxy once, then fetches from the origin, as the Go SDK
// does (core/sdk's goSDKDefaultGoProxy). go.sum still verifies the result.
const seedGoProxy = "https://proxy.golang.org|https://proxy.golang.org|direct"

// GoSDKVersions is what go-sdk's versions.toml names that the engine
// image's builds for go-sdk's modules depend on.
type GoSDKVersions struct {
	// DefaultTarget is the dagger.io/dagger release a new module pins.
	DefaultTarget string `toml:"defaultTarget"`
	// DaggerGo is the dagger/go its modules build with.
	DaggerGo string `toml:"daggerGo"`
}

// ReadGoSDKVersions reads go-sdk's versions.toml from goSDK, go-sdk's tree.
func ReadGoSDKVersions(ctx context.Context, goSDK *dagger.Directory) (GoSDKVersions, error) {
	var versions GoSDKVersions
	contents, err := goSDK.File("versions.toml").Contents(ctx)
	if err != nil {
		return versions, err
	}
	if err := toml.Unmarshal([]byte(contents), &versions); err != nil {
		return versions, fmt.Errorf("parse go-sdk's versions.toml: %w", err)
	}
	return versions, nil
}

// GoSDKImageEntriesSeed is the Go module whose dependencies' builds the
// engine image ships for the modules go-sdk generates: one that imports
// what their generated code imports, tidied with the dagger.io/dagger
// version they pin, libVersion (go-sdk's defaultTarget), and sdk/go's
// requirements. GoImageEntriesSeed has the standard library's.
//
// goSDK is go-sdk's tree. Its generated-imports.txt lists what a new
// module's generated code imports besides the standard library, which
// go-sdk's end-to-end checks hold to its generator's output.
//
// The seed requires sdk/go's raised requirements itself until a release of
// dagger.io/dagger carries them. Then go-sdk's defaultTarget names that
// release, whose go.mod requires them, and the seed needs no requirements of
// its own. Until then a new go-sdk module on the released target's lower
// requirements misses these builds.
func (build *Builder) GoSDKImageEntriesSeed(ctx context.Context, goSDK *dagger.Directory, libVersion string) (*dagger.Directory, error) {
	importList, err := goSDK.File("generated-imports.txt").Contents(ctx)
	if err != nil {
		return nil, err
	}
	var imports []string
	for _, line := range strings.Split(importList, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			imports = append(imports, line)
		}
	}
	sdkGoMod, err := build.source.File("sdk/go/go.mod").Contents(ctx)
	if err != nil {
		return nil, err
	}
	sdkMod, err := modfile.Parse("go.mod", []byte(sdkGoMod), nil)
	if err != nil {
		return nil, fmt.Errorf("parse sdk/go/go.mod: %w", err)
	}
	seedMod := new(modfile.File)
	if err := seedMod.AddModuleStmt("seed"); err != nil {
		return nil, err
	}
	if err := seedMod.AddGoStmt(distconsts.GolangVersion); err != nil {
		return nil, err
	}
	for _, req := range sdkMod.Require {
		seedMod.AddNewRequire(req.Mod.Path, req.Mod.Version, req.Indirect)
	}
	goMod, err := seedMod.Format()
	if err != nil {
		return nil, err
	}
	var mainGo strings.Builder
	mainGo.WriteString("package main\n\nimport (\n")
	for _, imp := range imports {
		fmt.Fprintf(&mainGo, "\t_ %q\n", imp)
	}
	mainGo.WriteString(")\n\nfunc main() {}\n")
	return core.NewContainer().
		From(consts.GolangImage).
		WithEnvVariable("GOPROXY", seedGoProxy).
		WithNewFile("/seed/go.mod", string(goMod)).
		WithNewFile("/seed/main.go", mainGo.String()).
		WithWorkdir("/seed").
		WithExec([]string{"go", "get", "dagger.io/dagger@" + libVersion}).
		WithExec([]string{"go", "mod", "tidy"}).
		Directory("/seed"), nil
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

	libVersion, err := goSDKLibVersion(ctx, source.File("core/sdk/go_sdk_module.go"))
	if err != nil {
		return nil, "", err
	}

	return core.NewDirectory().
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
// codegen pin: the goSDKLibVersion constant in core/sdk/go_sdk_module.go.
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
	return "", fmt.Errorf("goSDKLibVersion constant not found in core/sdk/go_sdk_module.go")
}
