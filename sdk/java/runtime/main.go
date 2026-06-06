// Runtime module for the Java SDK

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	_ "embed"

	"java-sdk/internal/dagger"

	"github.com/iancoleman/strcase"
)

const (
	ModSourceDirPath = "/src"
	ModDirPath       = "/opt/module"
	GenPath          = "/dagger-io"
	// VendorDir is the directory, relative to the module root, where the Java
	// SDK is vendored as real, buildable source by `dagger develop`.
	VendorDir = "sdk"
	// GeneratedSrcDir is the directory, relative to the module root, where
	// `dagger develop` vendors the generated entrypoint
	// (io.dagger.gen.entrypoint.Entrypoint) as real, buildable source. Its java/
	// subdirectory is a regular compilation root (see template/pom.xml), so a
	// plain `mvn package` compiles it without re-running the processor. It is
	// generated and git-ignored, like the vendored sdk/src tree.
	GeneratedSrcDir = "src/generated"

	// annotationsDir is where the Maven compiler plugin writes annotation-processed
	// sources (the entrypoint) during an engine build (proc=full). The engine
	// relocates it to GeneratedSrcDir/java for export.
	annotationsDir = "target/generated-sources/annotations"

	// procFull enables the Dagger annotation processor (entrypoint generation)
	// for an engine-driven build; a plain `mvn package` defaults to "none".
	procFull = "-Ddagger.proc=full"

	javaSdkModuleNameEnv = "_DAGGER_JAVA_SDK_MODULE_NAME"

	processorServiceFile = "META-INF/services/javax.annotation.processing.Processor"
	processorClassName   = "io.dagger.annotation.processor.DaggerModuleAnnotationProcessor"
)

type JavaSdk struct {
	SDKSourceDir *dagger.Directory
	moduleConfig moduleConfig
	// If true, -e flag will be added to the maven command to display execution error messages
	MavenErrors bool
	// If true, -X flag will be added to the maven command to enable full debug logging
	MavenDebugLogging bool
}

type moduleConfig struct {
	name    string
	subPath string
	dirPath string
}

func (c *moduleConfig) modulePath() string {
	return filepath.Join(ModSourceDirPath, c.subPath)
}

func New(
	// Directory with the Java SDK source code.
	// dagger-java-samples is not necessary to build, but as it's referenced in the root pom.xml maven
	// will check if it's there. So we keep the pom.xml to fake it.
	// +defaultPath="/sdk/java"
	// +ignore=["**", "!dagger-codegen-maven-plugin/", "!dagger-java-annotation-processor/", "!dagger-java-sdk/", "!dagger-java-samples/pom.xml", "!LICENSE", "!README.md", "!pom.xml", "**/src/test", "**/target"]
	sdkSourceDir *dagger.Directory,
) (*JavaSdk, error) {
	if sdkSourceDir == nil {
		return nil, fmt.Errorf("sdk source directory not provided")
	}
	return &JavaSdk{
		SDKSourceDir: sdkSourceDir,
		MavenErrors:  false,
	}, nil
}

func (m *JavaSdk) WithConfig(
	// +default=false
	mavenErrors bool,
	// +default=false
	mavenDebugLogging bool,
) *JavaSdk {
	m.MavenErrors = mavenErrors
	m.MavenDebugLogging = mavenDebugLogging
	return m
}

func (m *JavaSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	if err := m.setModuleConfig(ctx, modSource); err != nil {
		return nil, err
	}
	ctr, err := m.moduleContainer(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	// Generate the entrypoint (io.dagger.gen.entrypoint.Entrypoint) as real source
	// under src/generated/java by running the vendored annotation processor over
	// the user module, and export it alongside the vendored SDK sources. A plain
	// `mvn package` then compiles it without re-running the processor.
	entrypoint, err := m.generateEntrypoint(ctx, ctr)
	if err != nil {
		return nil, err
	}

	genDir := dag.Directory().
		WithDirectory("/", ctr.Directory(ModSourceDirPath)).
		WithDirectory(filepath.Join(m.moduleConfig.subPath, GeneratedSrcDir, "java"), entrypoint)

	return dag.
		GeneratedCode(genDir).
		WithVCSGeneratedPaths([]string{
			filepath.Join(VendorDir, "src", "**"),
			filepath.Join(GeneratedSrcDir, "**"),
		}).
		WithVCSIgnoredPaths([]string{
			filepath.Join(VendorDir, "src"),
			GeneratedSrcDir,
			"target",
		}), nil
}

// generateEntrypoint compiles the user module with the Dagger annotation
// processor enabled (proc=full) so it generates io.dagger.gen.entrypoint.Entrypoint,
// and returns the generated-sources directory (the entrypoint) as real, buildable
// source for the caller to vendor under src/generated/java.
//
// This mirrors the two-pass build performed when packaging the jar, but stops
// after `compile` so we only pay for source generation, not packaging.
func (m *JavaSdk) generateEntrypoint(
	ctx context.Context,
	ctr *dagger.Container,
) (*dagger.Directory, error) {
	compiled := ctr.
		// set the module name as an environment variable so we ensure constructor is only on main object
		WithEnvVariable(javaSdkModuleNameEnv, m.moduleConfig.name).
		WithExec(m.mavenCommand("mvn", "clean", "compile", procFull))
	return compiled.Directory(filepath.Join(m.moduleConfig.modulePath(), annotationsDir)), nil
}

// moduleContainer returns a maven container with the user module sources, the
// vendored Java SDK sources (the hand-written library, the annotation processor
// and the client bindings generated from the engine schema) and a pom.xml.
//
// Everything needed to build the module is present as real source, so a single
// `mvn package` is enough: there is no need to build or install the SDK as
// separate Maven artifacts first.
func (m *JavaSdk) moduleContainer(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	vendoredSDK, err := m.vendoredSDK(ctx, introspectionJSON)
	if err != nil {
		return nil, err
	}

	ctr, err := m.mvnContainer(ctx)
	if err != nil {
		return nil, err
	}
	ctr = ctr.
		WithMountedCache("/root/.m2", dag.CacheVolume("sdk-java-maven-m2"), dagger.ContainerWithMountedCacheOpts{Sharing: dagger.CacheSharingModeLocked}).
		// Copy the user module directory under /src
		WithDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		// Set the working directory to the one containing the sources to build, not just the module root
		WithWorkdir(m.moduleConfig.modulePath())

	// Add a default template if there's no existing user code
	ctr, err = m.addTemplate(ctx, ctr)
	if err != nil {
		return nil, err
	}

	// Vendor the SDK as real source next to the user module, replacing any
	// stale content from a previous `dagger develop`. Only the generated
	// sdk/src subtree is managed by Dagger, so we leave the rest of sdk/
	// untouched (e.g. a locally referenced SDK checkout at sdk/java).
	vendorPath := filepath.Join(m.moduleConfig.modulePath(), VendorDir)
	ctr = ctr.
		WithoutDirectory(filepath.Join(vendorPath, "src")).
		WithDirectory(vendorPath, vendoredSDK).
		// Drop any entrypoint left over from a previous `dagger develop`; an
		// engine-driven build regenerates it (proc=full) from the current code.
		WithoutDirectory(filepath.Join(m.moduleConfig.modulePath(), GeneratedSrcDir))

	return ctr, nil
}

// vendoredSDK returns the Java SDK as buildable source, laid out under a single
// directory so it can be dropped next to the user module:
//
//	sdk/src/main/java          the hand-written SDK library
//	sdk/src/processor/java     the annotation processor that generates the entrypoint
//	sdk/src/generated/java     the client bindings generated from the engine schema
//	sdk/src/processor/resources the annotation processor service descriptor
//
// The only thing that needs to be built ahead of time is the codegen Maven
// plugin, which turns the introspection schema into the client bindings.
func (m *JavaSdk) vendoredSDK(
	ctx context.Context,
	introspectionJSON *dagger.File,
) (*dagger.Directory, error) {
	ctr, err := m.mvnContainer(ctx)
	if err != nil {
		return nil, err
	}
	built := ctr.
		// Cache maven dependencies
		WithMountedCache("/root/.m2", dag.CacheVolume("sdk-java-maven-m2"), dagger.ContainerWithMountedCacheOpts{Sharing: dagger.CacheSharingModeLocked}).
		// Mount the introspection JSON file used to generate the client bindings
		WithMountedFile("/schema.json", introspectionJSON).
		// Copy the SDK source directory, with all the files needed to generate the bindings
		WithDirectory(GenPath, m.SDKSourceDir).
		WithWorkdir(GenPath).
		// Build and install the codegen Maven plugin. It is a build-time only tool
		// (it converts the introspection schema into Java client bindings) and is
		// the same regardless of the module, so it benefits from the shared m2 cache.
		WithExec(m.mavenCommand(
			"mvn",
			"--projects", "dagger-codegen-maven-plugin", "--also-make",
			"clean", "install",
			"-DskipTests",
			"-Dfmt.skip=true",
		)).
		// Generate the client bindings from the introspection schema.
		WithExec(m.mavenCommand(
			"mvn",
			"--projects", "dagger-java-sdk",
			"generate-sources",
			"-Ddaggerengine.schema=/schema.json",
			"-Dfmt.skip=true",
		))

	return dag.Directory().
		WithDirectory(
			filepath.Join("src", "main", "java"),
			built.Directory(filepath.Join(GenPath, "dagger-java-sdk", "src", "main", "java"))).
		WithDirectory(
			filepath.Join("src", "processor", "java"),
			built.Directory(filepath.Join(GenPath, "dagger-java-annotation-processor", "src", "main", "java"))).
		WithDirectory(
			filepath.Join("src", "generated", "java"),
			built.Directory(filepath.Join(GenPath, "dagger-java-sdk", "target", "generated-sources", "dagger"))).
		WithNewFile(
			filepath.Join("src", "processor", "resources", processorServiceFile),
			processorClassName+"\n"), nil
}

// legacyPom reports whether an existing pom.xml predates the vendored-source
// layout: such poms depend on the (never published) io.dagger SDK artifacts via
// the dagger.module.deps version dance and cannot be built by the current SDK.
func legacyPom(content string) bool {
	return strings.Contains(content, "dagger.module.deps") ||
		strings.Contains(content, "dagger-java-sdk")
}

// addTemplate creates all the necessary files to start a new Java module.
//
// For a brand new module (no pom.xml) it lays down the full starter template.
// For an existing module whose pom.xml predates the vendored-source layout it
// regenerates only the pom.xml so the module keeps building, leaving the user
// sources untouched. An up-to-date pom.xml is left as-is.
func (m *JavaSdk) addTemplate(
	ctx context.Context,
	ctr *dagger.Container,
) (*dagger.Container, error) {
	name := m.moduleConfig.name
	pkgName := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(name), "-", ""), "_", "")
	kebabName := strcase.ToKebab(name)
	camelName := strcase.ToCamel(name)

	pomExists := false
	if content, err := ctr.File(filepath.Join(m.moduleConfig.modulePath(), "pom.xml")).Contents(ctx); err == nil {
		pomExists = true
		if !legacyPom(content) {
			// pom.xml already uses the vendored-source layout, keep it as-is
			return ctr, nil
		}
	}

	absPath := func(rel ...string) string {
		return filepath.Join(append([]string{m.moduleConfig.modulePath()}, rel...)...)
	}

	changes := []repl{
		{"dagger-module-placeholder", kebabName},
		{"daggermoduleplaceholder", pkgName},
	}

	// Edit template content so that they match the dagger module name
	templateDir := dag.CurrentModule().Source().Directory("template")
	pomXML, err := m.replace(ctx, templateDir,
		"pom.xml", changes...)
	if err != nil {
		return ctr, fmt.Errorf("could not add template: %w", err)
	}

	// Always (re)generate the pom.xml so it uses the vendored-source layout
	ctr = ctr.WithNewFile(absPath("pom.xml"), pomXML)

	// Only lay down the starter sources for a brand new module; for an existing
	// module we just migrated the pom.xml and must keep the user's own sources.
	if pomExists {
		return ctr, nil
	}

	changes = append(changes, repl{"DaggerModule", camelName})
	daggerModuleJava, err := m.replace(ctx, templateDir,
		filepath.Join("src", "main", "java", "io", "dagger", "modules", "daggermodule", "DaggerModule.java"),
		changes...)
	if err != nil {
		return ctr, fmt.Errorf("could not add template: %w", err)
	}
	packageInfoJava, err := m.replace(ctx, templateDir,
		filepath.Join("src", "main", "java", "io", "dagger", "modules", "daggermodule", "package-info.java"),
		changes...)
	if err != nil {
		return ctr, fmt.Errorf("could not add template: %w", err)
	}

	// And copy them to the container, renamed to match the dagger module name
	ctr = ctr.
		WithNewFile(absPath("src", "main", "java", "io", "dagger", "modules", pkgName, fmt.Sprintf("%s.java", camelName)), daggerModuleJava).
		WithNewFile(absPath("src", "main", "java", "io", "dagger", "modules", pkgName, "package-info.java"), packageInfoJava)

	return ctr, nil
}

func (m *JavaSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	if err := m.setModuleConfig(ctx, modSource); err != nil {
		return nil, err
	}

	// Get a container with the user module sources and the vendored SDK sources
	mvnCtr, err := m.moduleContainer(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}
	// Build the executable jar
	jar, err := m.buildJar(ctx, mvnCtr)
	if err != nil {
		return nil, err
	}

	javaCtr, err := m.jreContainer(ctx)
	if err != nil {
		return nil, err
	}
	javaCtr = javaCtr.
		WithFile(filepath.Join(ModDirPath, "module.jar"), jar).
		WithWorkdir(ModDirPath).
		WithEntrypoint([]string{"java", "-jar", filepath.Join(ModDirPath, "module.jar")})

	return javaCtr, nil
}

// buildJar builds and returns the generated jar from the user module
func (m *JavaSdk) buildJar(
	ctx context.Context,
	ctr *dagger.Container,
) (*dagger.File, error) {
	return m.finalJar(ctx,
		ctr.
			// set the module name as an environment variable so we ensure constructor is only on main object
			WithEnvVariable(javaSdkModuleNameEnv, m.moduleConfig.name).
			// build the final jar, (re)generating the entrypoint from the current
			// module code (proc=full) in the same pass
			WithExec(m.mavenCommand(
				"mvn",
				"clean",
				"package",
				"-DskipTests",
				procFull,
			)))
}

// finalJar will return the jar corresponding to the user module built
// In order to have the final container as minimal as possible, we just want to be able to run a jar.
// To make it easy, we will rename the jar as module.jar
// But that's not easy, as we don't know the name of the built jar, so we will ask maven to give us the
// artifactId and the version so we can get the jar name
func (m *JavaSdk) finalJar(
	ctx context.Context,
	ctr *dagger.Container,
) (*dagger.File, error) {
	artifactID, err := ctr.
		WithExec(m.mavenCommand("mvn", "org.apache.maven.plugins:maven-help-plugin:3.2.0:evaluate", "-Dexpression=project.artifactId", "-q", "-DforceStdout")).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	version, err := ctr.
		WithExec(m.mavenCommand("mvn", "org.apache.maven.plugins:maven-help-plugin:3.2.0:evaluate", "-Dexpression=project.version", "-q", "-DforceStdout")).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	jarFileName := fmt.Sprintf("%s-%s.jar", artifactID, version)

	return ctr.File(filepath.Join(m.moduleConfig.modulePath(), "target", jarFileName)), nil
}

func (m *JavaSdk) mvnContainer(ctx context.Context) (*dagger.Container, error) {
	return disableSVEOnArm64(ctx, m.MavenImage())
}

func (m *JavaSdk) jreContainer(ctx context.Context) (*dagger.Container, error) {
	return disableSVEOnArm64(ctx, m.JavaImage())
}

func disableSVEOnArm64(ctx context.Context, ctr *dagger.Container) (*dagger.Container, error) {
	if platform, err := ctr.Platform(ctx); err != nil {
		return nil, err
	} else if strings.Contains(string(platform), "arm64") {
		return ctr.WithEnvVariable("_JAVA_OPTIONS", "-XX:UseSVE=0"), nil
	}
	return ctr, nil
}

func (m *JavaSdk) setModuleConfig(ctx context.Context, modSource *dagger.ModuleSource) error {
	modName, err := modSource.ModuleName(ctx)
	if err != nil {
		return err
	}
	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return err
	}
	var dirPath string
	if kind, err := modSource.Kind(ctx); err != nil {
		return err
	} else if kind == dagger.ModuleSourceKindLocal {
		dirPath, err = modSource.LocalContextDirectoryPath(ctx)
		if err != nil {
			return err
		}
	}
	m.moduleConfig = moduleConfig{
		name:    modName,
		subPath: subPath,
		dirPath: dirPath,
	}

	return nil
}

type repl struct {
	oldString string
	newString string
}

func (m *JavaSdk) replace(
	ctx context.Context,
	dir *dagger.Directory,
	path string,
	changes ...repl,
) (string, error) {
	content, err := dir.File(path).Contents(ctx)
	if err != nil {
		return "", err
	}
	for _, change := range changes {
		content = strings.ReplaceAll(content, change.oldString, change.newString)
	}
	return content, nil
}

func (m *JavaSdk) mavenCommand(args ...string) []string {
	if m.MavenErrors {
		args = append(args, "-e")
	}
	if m.MavenDebugLogging {
		args = append(args, "-X")
	}
	args = append(args, "--threads", "1C", "--no-transfer-progress")
	return args
}

//go:embed images/maven/Dockerfile
var mavenImage string

func (m *JavaSdk) MavenImage() *dagger.Container {
	return dag.Directory().WithNewFile("Dockerfile", mavenImage).DockerBuild()
}

//go:embed images/java/Dockerfile
var javaImage string

func (m *JavaSdk) JavaImage() *dagger.Container {
	return dag.Directory().WithNewFile("Dockerfile", javaImage).DockerBuild()
}
