// Runtime module for the Java SDK

package main

import (
	"context"
	"fmt"
	"path/filepath"

	"java-sdk/internal/dagger"
)

const (
	MavenImage  = "maven:3.9.9-eclipse-temurin-23-alpine"
	MavenDigest = "sha256:77fe6f79f868484d85679bfaa121b34177e3fedf570086ea6babbd6db2223e89"
	JavaImage   = "eclipse-temurin:23-jre-alpine"
	JavaDigest  = "sha256:bd8e2c8c19bcadbaa8c6a128051a22384c6f7cfe5fa520cb663fe21fff96f084"

	ModSourceDirPath = "/src"
	ModDirPath       = "/opt/module"
	GenPath          = "sdk"
)

type JavaSdk struct {
	SDKSourceDir  *dagger.Directory
	RequiredPaths []string
	moduleConfig  moduleConfig
}

type moduleConfig struct {
	name    string
	subPath string
}

func (c *moduleConfig) modulePath() string {
	return filepath.Join(ModSourceDirPath, c.subPath)
}

func (c *moduleConfig) sdkPath() string {
	return filepath.Join(c.modulePath(), GenPath)
}

func New(
	// Directory with the Java SDK source code.
	// dagger-java-samples is not necessary to build, but as it's referenced in the root pom.xml maven
	// will check if it's there. So we keep it, until a better solution (like to extract the samples somewhere else)
	// +defaultPath="/sdk/java"
	// +ignore=["**", "!dagger-codegen-maven-plugin/", "!dagger-java-annotation-processor/", "!dagger-java-sdk/", "!dagger-java-samples", "!LICENSE", "!README.md", "!pom.xml"]
	sdkSourceDir *dagger.Directory,
) (*JavaSdk, error) {
	if sdkSourceDir == nil {
		return nil, fmt.Errorf("sdk source directory not provided")
	}
	return &JavaSdk{
		RequiredPaths: []string{},
		SDKSourceDir:  sdkSourceDir,
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
		GeneratedCode(dag.Directory().WithDirectory("/", mvnCtr.Directory(ModSourceDirPath))).
		WithVCSGeneratedPaths([]string{
			GenPath + "/**",
			//			"pom.xml",
		}).
		WithVCSIgnoredPaths([]string{
			GenPath,
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
		mvnContainer().
		// Mount the maven folder where we installed all the generated dependencies
		WithMountedDirectory("/root/.m2", m.buildJavaDependencies(introspectionJSON)).
		// Copy the user module directory under /src
		WithDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		// Set the working directory to the one containing the sources to build, not just the module root
		WithWorkdir(m.moduleConfig.modulePath())

	return ctr, nil
}

// buildJavaDependencies builds and install the needed dependencies
// used to build, package and run the user module.
// Everything will be done under ModSourceDirPath/<subPath>/sdk (m.moduleConfig.sdkPath()).
// There's no real necessity here, it's just a choice to keep it close to the rest but the only
// thing we care about is the resulting /root/.m2 folder
func (m *JavaSdk) buildJavaDependencies(
	introspectionJSON *dagger.File,
) *dagger.Directory {
	return m.
		// We need maven to build the dependencies
		mvnContainer().
		// Mount the introspection JSON file used to generate the SDK
		WithMountedFile("/schema.json", introspectionJSON).
		// Copy the SDK source directory, so all the files needed to build the dependencies
		WithDirectory(m.moduleConfig.sdkPath(), m.SDKSourceDir).
		WithWorkdir(m.moduleConfig.sdkPath()).
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
		}).
		Directory("/root/.m2")
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

	javaCtr := m.jreContainer().
		WithFile(filepath.Join(ModDirPath, "module.jar"), jar).
		WithWorkdir(ModDirPath).
		WithEntrypoint([]string{"java", "-XX:UseSVE=0", "-jar", filepath.Join(ModDirPath, "module.jar")})

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
	jarFileName := fmt.Sprintf("%s-%s.jar", artifactID, version)

	return ctr.File(filepath.Join(m.moduleConfig.modulePath(), "target", jarFileName)), nil
}

func (m *JavaSdk) mvnContainer() *dagger.Container {
	return dag.
		Container().
		From(fmt.Sprintf("%s@%s", MavenImage, MavenDigest))
}

func (m *JavaSdk) jreContainer() *dagger.Container {
	return dag.
		Container().
		From(fmt.Sprintf("%s@%s", JavaImage, JavaDigest))
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
