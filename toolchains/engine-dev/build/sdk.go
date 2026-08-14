package build

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"runtime"
	"strings"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/engine/distconsts"

	"dagger/engine-dev/consts"
	"dagger/engine-dev/internal/dagger"

	"github.com/dagger/dagger/sdk/typescript/runtime/tsdistconsts"
)

type sdkContent struct {
	index         ocispecs.Index
	sdkDir        *dagger.Directory
	envName       string
	extraEnv      map[string]string
	sdkDependency sdkDependencyCoordinates
}

type sdkDependencyCoordinates struct {
	Source       string `json:"source"`
	Registry     string `json:"registry,omitempty"`
	PackageName  string `json:"package"`
	ExactVersion string `json:"exact_version,omitempty"`
	URL          string `json:"url,omitempty"`
	Revision     string `json:"revision,omitempty"`
}

// Directory returns the complete OCI content-store layout.
func (content *sdkContent) Directory() *dagger.Directory {
	return content.sdkDir
}

// ManifestDigest returns the immutable manifest selected by the engine loader.
func (content *sdkContent) ManifestDigest() string {
	return content.index.Manifests[0].Digest.String()
}

// DescriptorDigest returns the domain-separated immutable source identity.
func (content *sdkContent) DescriptorDigest() string {
	return content.extraEnv[distconsts.RustSDKDescriptorDigestEnvName]
}

func (content *sdkContent) DependencyDescriptor() (string, error) {
	encoded, err := json.Marshal(content.sdkDependency)
	if err != nil {
		return "", fmt.Errorf("encode Rust SDK dependency descriptor: %w", err)
	}
	return string(encoded), nil
}

func (content *sdkContent) apply(ctr *dagger.Container) *dagger.Container {
	manifest := content.index.Manifests[0]
	manifestDgst := manifest.Digest.String()

	ctr = ctr.
		WithEnvVariable(content.envName, manifestDgst).
		WithDirectory(distconsts.EngineContainerBuiltinContentDir, content.sdkDir, dagger.ContainerWithDirectoryOpts{
			Include: []string{"blobs/"},
		})
	for name, value := range content.extraEnv {
		ctr = ctr.WithEnvVariable(name, value)
	}
	return ctr
}

const (
	rustSDKBuildImage         = "rust:1.97.1-bookworm@sha256:705e294093973d7c10e83400393dce7b3611f8e03e55a80af7fff6d02ae1affb"
	canonicalDaggerRepository = "https://github.com/dagger/dagger"
)

type rustTargetDescriptor struct {
	DaggerRepository string `json:"dagger_repository"`
	DaggerRevision   string `json:"dagger_revision"`
	EngineVersion    string `json:"engine_version"`
	RustSDKVersion   string `json:"rust_sdk_version"`
	RustVersion      string `json:"rust_version"`
	SchemaDigest     string `json:"schema_digest"`
}

// RustSDKContent builds and seals the module-backed Rust SDK content used by the
// built-in loader. Private source and Cargo target state remain build inputs only.
func (build *Builder) RustSDKContent(
	ctx context.Context,
	dependencyRepository string,
	dependencyRevision string,
) (*sdkContent, error) {
	if err := validateDigestPinnedImage(rustSDKBuildImage); err != nil {
		return nil, err
	}
	if len(build.vcsCommit) != 40 {
		return nil, fmt.Errorf("Rust SDK provenance requires a full Dagger revision")
	}
	repository := canonicalEngineRepository(build.vcsRepository)
	if repository == "" {
		return nil, fmt.Errorf("Rust SDK provenance requires an immutable repository origin")
	}

	var target rustTargetDescriptor
	targetContents, err := build.source.File("sdk/rust/completeness/target.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Rust SDK checked target: %w", err)
	}
	if err := json.Unmarshal([]byte(targetContents), &target); err != nil {
		return nil, fmt.Errorf("decode Rust SDK checked target: %w", err)
	}
	if target.DaggerRepository != "github.com/dagger/dagger" || len(target.DaggerRevision) != 40 ||
		target.RustVersion != "1.97.1" || target.RustSDKVersion == "" || target.SchemaDigest == "" {
		return nil, fmt.Errorf("Rust SDK checked target is incomplete or uses another toolchain")
	}
	requestedVersion := strings.TrimPrefix(build.version, "v")
	targetVersion := strings.TrimPrefix(target.EngineVersion, "v")
	if requestedVersion != "" && requestedVersion != targetVersion {
		return nil, fmt.Errorf("Rust SDK target engine version %q differs from build version %q", targetVersion, requestedVersion)
	}
	rustTarget, err := rustSDKTargetTriple(build.platformSpec.Architecture)
	if err != nil {
		return nil, err
	}
	rustfmtPath := fmt.Sprintf(
		"/usr/local/rustup/toolchains/%s-%s/bin/rustfmt",
		target.RustVersion,
		rustTarget,
	)

	dependencyKind := "registry"
	dependencyValue := target.RustSDKVersion
	sdkDependency := sdkDependencyCoordinates{
		Source:       dependencyKind,
		Registry:     "crates-io",
		PackageName:  "dagger-sdk",
		ExactVersion: dependencyValue,
	}
	switch {
	case dependencyRepository == "" && dependencyRevision == "" && repository == canonicalDaggerRepository:
	case dependencyRepository == "" || dependencyRevision == "":
		return nil, fmt.Errorf("Rust SDK Git dependency requires both repository and full revision")
	case dependencyRepository != "" && dependencyRevision != "":
		dependencyRepository, err = validateRustSDKGitDependency(
			ctx,
			build.source.Directory("sdk/rust/crates/dagger-sdk"),
			dependencyRepository,
			dependencyRevision,
		)
		if err != nil {
			return nil, err
		}
		dependencyKind = "git"
		dependencyValue = dependencyRevision
		sdkDependency = sdkDependencyCoordinates{
			Source:      dependencyKind,
			PackageName: "dagger-sdk",
			URL:         dependencyRepository,
			Revision:    dependencyRevision,
		}
	default:
		return nil, fmt.Errorf("fork-backed Rust SDK content requires an explicit reachable dependency revision")
	}

	rustWorkspace := build.source.Directory("sdk/rust").Filter(dagger.DirectoryFilterOpts{
		Include: []string{
			"Cargo.toml", "Cargo.lock", "rust-toolchain.toml",
			"completeness/target.json", "completeness/snapshots/schema.json",
			"crates/**/Cargo.toml", "crates/**/src/**/*.rs", "crates/**/assets/**",
		},
	})
	buildCtr := dag.Container(dagger.ContainerOpts{Platform: build.platform}).
		From(rustSDKBuildImage).
		WithDirectory("/src", rustWorkspace).
		WithWorkdir("/src").
		WithExec([]string{
			"rustup", "component", "add", "rustfmt", "--toolchain", target.RustVersion,
		}).
		WithExec([]string{
			"cargo", "build", "--release", "--locked", "--package", "dagger-sdk-engine", "--bin", "dagger-rust-engine",
		})

	runtimeSource := build.source.Directory("sdk/rust/runtime").Filter(dagger.DirectoryFilterOpts{
		Include: []string{
			"dagger-module.toml", "go.mod", "go.sum", "main.go", "runtime.go", "dagger.gen.go", "internal/**/*.go",
		},
		Exclude: []string{"**/*_test.go"},
	})
	rootfs := dag.Directory().
		WithDirectory("runtime", runtimeSource).
		WithFile("dist/dagger-rust-engine", buildCtr.File("/src/target/release/dagger-rust-engine"), dagger.DirectoryWithFileOpts{Permissions: 0o755}).
		WithFile("dist/rustfmt", buildCtr.File(rustfmtPath), dagger.DirectoryWithFileOpts{Permissions: 0o755}).
		WithFile("dist/client-generation.json", build.source.File("sdk/rust/crates/dagger-codegen/assets/client-generation.json")).
		WithFile("dist/runtime-policy.json", build.source.File("sdk/rust/runtime/assets/runtime-policy.json")).
		WithFile("LICENSE", build.source.File("LICENSE"))

	packageArgs := []string{
		"/src/target/release/dagger-rust-engine", "package-content",
		"--root", "/content",
		"--repository", "https://" + target.DaggerRepository,
		"--dagger-revision", target.DaggerRevision,
		"--engine-version", targetVersion,
		"--rust-sdk-version", target.RustSDKVersion,
		"--rust-toolchain", target.RustVersion,
		"--core-schema-digest", target.SchemaDigest,
		"--dependency-kind", dependencyKind,
		"--dependency-value", dependencyValue,
	}
	if dependencyKind == "git" {
		// Fork provenance belongs to the generated public dependency. The
		// private compiler target remains the reviewed canonical target.
		packageArgs = append(packageArgs, "--dependency-repository", dependencyRepository)
	}
	sealedCtr := buildCtr.
		// A scratch mount makes the package tool's writes available as a clean
		// directory output. Taking a subdirectory from the build container itself
		// would retain builder layers and their original /content path in the OCI image.
		WithMountedDirectory("/content", rootfs).
		WithExec(packageArgs)
	descriptorDigest, err := sealedCtr.Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("seal Rust SDK content: %w", err)
	}
	descriptorDigest = strings.TrimSpace(descriptorDigest)
	if !isCanonicalDigest(descriptorDigest) {
		return nil, fmt.Errorf("Rust SDK descriptor identity is absent or malformed")
	}
	sealedRoot := sealedCtr.Directory("/content")

	sdkCtrTarball := dag.Container(dagger.ContainerOpts{Platform: build.platform}).
		WithRootfs(sealedRoot).
		AsTarball(dagger.ContainerAsTarballOpts{ForcedCompression: dagger.ImageLayerCompressionZstd})
	sdkDir := unpackTar(sdkCtrTarball)
	var index ocispecs.Index
	indexContents, err := sdkDir.File("index.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Rust SDK OCI index: %w", err)
	}
	if err := json.Unmarshal([]byte(indexContents), &index); err != nil {
		return nil, fmt.Errorf("decode Rust SDK OCI index: %w", err)
	}
	if len(index.Manifests) != 1 {
		return nil, fmt.Errorf("Rust SDK OCI content must contain exactly one manifest")
	}
	dependencyDescriptor, err := json.Marshal(sdkDependency)
	if err != nil {
		return nil, fmt.Errorf("encode Rust SDK dependency descriptor: %w", err)
	}
	dependencyDescriptorDigest := sha256.Sum256(dependencyDescriptor)
	return &sdkContent{
		index:         index,
		sdkDir:        sdkDir,
		envName:       distconsts.RustSDKManifestDigestEnvName,
		sdkDependency: sdkDependency,
		extraEnv: map[string]string{
			distconsts.RustSDKDescriptorDigestEnvName:     descriptorDigest,
			distconsts.RustSDKDependencyDescriptorEnvName: string(dependencyDescriptor),
			distconsts.RustSDKDependencyDigestEnvName:     fmt.Sprintf("sha256:%x", dependencyDescriptorDigest),
		},
	}, nil
}

func validateRustSDKGitDependency(
	ctx context.Context,
	localPackage *dagger.Directory,
	repository string,
	revision string,
) (string, error) {
	repository = canonicalEngineRepository(repository)
	parsed, err := url.Parse(repository)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("Rust SDK dependency repository must be a credential-free HTTPS URL")
	}
	if len(revision) != 40 {
		return "", fmt.Errorf("Rust SDK dependency revision must be one full commit")
	}
	for _, character := range revision {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return "", fmt.Errorf("Rust SDK dependency revision must be lowercase hexadecimal")
		}
	}

	// Examples, tests, and crate prose stay outside the focused engine source so
	// editing them cannot rebuild packaged toolchain content. The manifest,
	// source, and assets are the closed input set Cargo can compile or embed.
	dependencySourceFilter := dagger.DirectoryFilterOpts{Include: []string{
		"Cargo.toml",
		"src/**",
		"assets/**",
	}}
	localPackage = localPackage.Filter(dependencySourceFilter)
	remotePackage := dag.Git(repository).
		Commit(revision).
		Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}).
		Directory("sdk/rust/crates/dagger-sdk").
		Filter(dependencySourceFilter)
	// Directory digests include source-root metadata, so a Git tree and an
	// identical workspace directory need not share one digest. Bidirectional
	// diffs compare the package entries themselves and expose both additions and
	// removals without weakening the immutable-revision check.
	localChanges, err := remotePackage.Diff(localPackage).Entries(ctx)
	if err != nil {
		return "", fmt.Errorf("compare local public Rust SDK package: %w", err)
	}
	remoteChanges, err := localPackage.Diff(remotePackage).Entries(ctx)
	if err != nil {
		return "", fmt.Errorf("compare remote public Rust SDK package: %w", err)
	}
	if len(localChanges) != 0 || len(remoteChanges) != 0 {
		return "", fmt.Errorf(
			"Rust SDK dependency revision does not match the public dagger-sdk package being built (local changes: %q; remote changes: %q)",
			localChanges,
			remoteChanges,
		)
	}
	return repository, nil
}

func rustSDKTargetTriple(architecture string) (string, error) {
	switch architecture {
	case "amd64":
		return "x86_64-unknown-linux-gnu", nil
	case "arm64":
		return "aarch64-unknown-linux-gnu", nil
	default:
		return "", fmt.Errorf("Rust SDK does not support engine architecture %q", architecture)
	}
}

func isCanonicalDigest(value string) bool {
	algorithm, encoded, found := strings.Cut(value, ":")
	if !found || algorithm != "sha256" || len(encoded) != 64 {
		return false
	}
	for _, character := range encoded {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func validateDigestPinnedImage(reference string) error {
	name, digest, found := strings.Cut(reference, "@sha256:")
	if !found || name == "" || len(digest) != 64 {
		return fmt.Errorf("image reference %q must include a complete sha256 digest", reference)
	}
	for _, character := range digest {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return fmt.Errorf("image reference %q has a non-canonical sha256 digest", reference)
		}
	}
	return nil
}

func canonicalEngineRepository(repository string) string {
	repository = strings.TrimSuffix(repository, ".git")
	if strings.HasPrefix(repository, "github.com/") {
		return "https://" + repository
	}
	return repository
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
		WithMountedDirectory("", pySrc.Directory("codegen")).
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
		Wolfi().
		Container().
		WithMountedDirectory("/out", dag.Directory()).
		WithMountedFile("/target.tar", tarball).
		WithExec([]string{"tar", "xf", "/target.tar", "-C", "/out"}).
		Directory("/out")
}
