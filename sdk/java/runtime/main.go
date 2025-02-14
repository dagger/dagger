// Runtime module for the Java SDK

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"java-sdk/internal/dagger"

	"github.com/iancoleman/strcase"
)

const (
	MavenImage  = "maven:3.9.9-eclipse-temurin-17"
	MavenDigest = "sha256:f8ac06fcc542020a0b3741e850c6b023c17f325e2a5fb6b81abbe67120364680"
	JavaImage   = "eclipse-temurin:23-jre-noble"
	JavaDigest  = "sha256:7003c5ac866cbf50af64ef563d203a939f1ab2869ec7d9f89c3f5009ee605452"

	ModSourceDirPath = "/src"
	ModDirPath       = "/opt/module"
	GenPath          = "dagger-io"
)

type JavaSdk struct {
	SDKSourceDir *dagger.Directory
	moduleConfig moduleConfig
}

type moduleConfig struct {
	name    string
	subPath string
}

func (c *moduleConfig) modulePath() string {
	return filepath.Join(ModSourceDirPath, c.subPath)
}

func (c *moduleConfig) genPath() string {
	return filepath.Join(ModSourceDirPath, GenPath)
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
	}, nil
}

func (m *JavaSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	if err := m.setModuleConfig(ctx, modSource); err != nil {
		return nil, err
	}
	mvnCtr, err := m.codegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	return dag.
		GeneratedCode(dag.Directory().WithDirectory("/", m.generateCode(ctx, mvnCtr, introspectionJSON))).
		WithVCSGeneratedPaths([]string{
			"target/generated-sources/**",
		}).
		WithVCSIgnoredPaths([]string{
			"target",
		}), nil
}

// codegenBase takes the user module code, add the generated SDK dependencies
// if the user module code is empty, creates a default module content based on the template from the SDK
// The generated container will *not* contain the SDK source code, but only the packages built from the SDK
func (m *JavaSdk) codegenBase(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	ctr := m.
		// We need maven to build the user module
		mvnContainer(ctx).
		// Mount the maven folder where we installed all the generated dependencies
		WithMountedDirectory("/root/.m2", m.buildJavaDependencies(ctx, introspectionJSON).Directory("/root/.m2")).
		// Copy the user module directory under /src
		WithDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		// Set the working directory to the one containing the sources to build, not just the module root
		WithWorkdir(m.moduleConfig.modulePath())
	// Add a default template if there's no existing user code
	return m.addTemplate(ctx, ctr)
}

// buildJavaDependencies builds and install the needed dependencies
// used to build, package and run the user module.
// Everything will be done under ModSourceDirPath/dagger-io (m.moduleConfig.genPath()).
func (m *JavaSdk) buildJavaDependencies(
	ctx context.Context,
	introspectionJSON *dagger.File,
) *dagger.Container {
	return m.
		// We need maven to build the dependencies
		mvnContainer(ctx).
		// Mount the introspection JSON file used to generate the SDK
		WithMountedFile("/schema.json", introspectionJSON).
		// Copy the SDK source directory, so all the files needed to build the dependencies
		WithDirectory(m.moduleConfig.genPath(), m.SDKSourceDir).
		WithWorkdir(m.moduleConfig.genPath()).
		// Build and install the java modules one by one
		// - dagger-codegen-maven-plugin: this plugin will be used to generate the SDK code, from the introspection file,
		//   this means including the ability to call other projects (not part of the main dagger SDK)
		//   - this plugin is only used to build the SDK, the user module doesn't need it
		// - dagger-java-annotation-processor: this will read dagger specific annotations (@Module, @Object, @Function)
		//   and generate the entrypoint to register the module and invoke the functions
		//   - this processor will be used by the user module to generate the entrypoint, so it's referenced in the user module pom.xml
		// - dagger-java-sdk: the actual SDK, where the generated code will be written
		//   - the user module code only depends on this, it includes all the required types
		WithExec([]string{
			"mvn",
			"--projects", "dagger-codegen-maven-plugin,dagger-java-annotation-processor,dagger-java-sdk", "--also-make",
			"clean", "install",
			// avoid tests
			"-DskipTests",
			// specify the introspection json file
			"-Ddaggerengine.schema=/schema.json",
			// "-e", // this is just for debug purpose, uncomment if needed
		})
}

// addTemplate creates all the necessary files to start a new Java module
func (m *JavaSdk) addTemplate(
	ctx context.Context,
	ctr *dagger.Container,
) (*dagger.Container, error) {
	name := m.moduleConfig.name
	pkgName := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(name), "-", ""), "_", "")
	kebabName := strcase.ToKebab(name)
	camelName := strcase.ToCamel(name)

	// Check if there's a pom.xml inside the module path. If a file exist, no need to add the templates
	if _, err := ctr.File(filepath.Join(m.moduleConfig.modulePath(), "pom.xml")).Name(ctx); err == nil {
		return ctr, nil
	}

	absPath := func(rel ...string) string {
		return filepath.Join(append([]string{m.moduleConfig.modulePath()}, rel...)...)
	}

	changes := []repl{
		{"dagger-module", kebabName},
		{"daggermodule", pkgName},
	}

	// Edit template content so that they match the dagger module name
	templateDir := dag.CurrentModule().Source().Directory("template")
	pomXML, err := m.replace(ctx, templateDir,
		"pom.xml", changes...)
	if err != nil {
		return ctr, fmt.Errorf("could not add template: %w", err)
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
		WithNewFile(absPath("pom.xml"), pomXML).
		WithNewFile(absPath("src", "main", "java", "io", "dagger", "modules", pkgName, fmt.Sprintf("%s.java", camelName)), daggerModuleJava).
		WithNewFile(absPath("src", "main", "java", "io", "dagger", "modules", pkgName, "package-info.java"), packageInfoJava)

	return ctr, nil
}

// generateCode builds and returns the generated source code and java classes
func (m *JavaSdk) generateCode(
	ctx context.Context,
	ctr *dagger.Container,
	introspectionJSON *dagger.File,
) *dagger.Directory {
	// generate the java sdk dependencies
	javaDeps := m.buildJavaDependencies(ctx, introspectionJSON)
	// generate the entrypoint class based on the user module
	entrypoint := ctr.WithExec([]string{"mvn", "clean", "compile"})
	return dag.
		Directory().
		// copy all user files
		WithDirectory(
			m.moduleConfig.modulePath(),
			ctr.Directory(m.moduleConfig.modulePath())).
		// copy the generated entrypoint under target/generated-sources/annotations
		WithDirectory(
			filepath.Join(m.moduleConfig.modulePath(), "target", "generated-sources", "entrypoint"),
			entrypoint.Directory(filepath.Join(m.moduleConfig.modulePath(), "target", "generated-sources", "annotations"))).
		// copy the sdk source code under target/generated-sources/dagger-io
		// this is not really generated-sources, this is the sdk. But we don't want it as the user source code
		// and we don't want to install it on the user machine. That way the java classes are made available
		// to a build system or an IDE without to interfere with the user source code
		WithDirectory(
			filepath.Join(m.moduleConfig.modulePath(), "target", "generated-sources", "dagger-io"),
			javaDeps.Directory(filepath.Join(m.moduleConfig.genPath(), "dagger-java-sdk", "src", "main", "java"))).
		// copy the generated SDK files to target/generated-sources/dagger-module
		// those are all the types generated from the introspection
		WithDirectory(
			filepath.Join(m.moduleConfig.modulePath(), "target", "generated-sources", "dagger-module"),
			javaDeps.Directory(filepath.Join(m.moduleConfig.genPath(), "dagger-java-sdk", "target", "generated-sources", "dagger"))).
		Directory(ModSourceDirPath)
}

func (m *JavaSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	if err := m.setModuleConfig(ctx, modSource); err != nil {
		return nil, err
	}

	// Get a container with the user module sources and the SDK packages built and installed
	mvnCtr, err := m.codegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}
	// Build the executable jar
	jar, err := m.buildJar(ctx, mvnCtr)
	if err != nil {
		return nil, err
	}

	javaCtr := m.jreContainer(ctx).
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
		ctr.WithExec([]string{"mvn", "clean", "package", "-DskipTests"}))
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
		WithExec([]string{"mvn", "org.apache.maven.plugins:maven-help-plugin:3.2.0:evaluate", "-Dexpression=project.artifactId", "-q", "-DforceStdout"}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	version, err := ctr.
		WithExec([]string{"mvn", "org.apache.maven.plugins:maven-help-plugin:3.2.0:evaluate", "-Dexpression=project.version", "-q", "-DforceStdout"}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	jarFileName := fmt.Sprintf("%s-%s.jar", artifactID, version)

	return ctr.File(filepath.Join(m.moduleConfig.modulePath(), "target", jarFileName)), nil
}

func (m *JavaSdk) mvnContainer(ctx context.Context) *dagger.Container {
	ctr := dag.
		Container().
		From(fmt.Sprintf("%s@%s", MavenImage, MavenDigest))
	if platform, err := ctr.Platform(ctx); err == nil && strings.Contains(string(platform), "arm64") {
		ctr = ctr.WithEnvVariable("MAVEN_OPTS", "-XX:UseSVE=0")
	}
	return ctr
}

func (m *JavaSdk) jreContainer(ctx context.Context) *dagger.Container {
	ctr := dag.
		Container().
		From(fmt.Sprintf("%s@%s", JavaImage, JavaDigest))
	if platform, err := ctr.Platform(ctx); err == nil && strings.Contains(string(platform), "arm64") {
		ctr = ctr.WithEnvVariable("JAVA_OPTS", "-XX:UseSVE=0")
	}
	return ctr
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
	m.moduleConfig = moduleConfig{
		name:    modName,
		subPath: subPath,
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
