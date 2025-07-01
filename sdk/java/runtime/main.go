// Runtime module for the Java SDK

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"java-sdk/internal/dagger"

	"github.com/iancoleman/strcase"
)

const (
	MavenImage  = "maven:3.9.9-eclipse-temurin-21-alpine"
	MavenDigest = "sha256:4cbb8bf76c46b97e028998f2486ed014759a8e932480431039bdb93dffe6813e"
	JavaImage   = "eclipse-temurin:21-jre-alpine-3.21"
	JavaDigest  = "sha256:4e9ab608d97796571b1d5bbcd1c9f430a89a5f03fe5aa6c093888ceb6756c502"

	ModSourceDirPath = "/src"
	ModDirPath       = "/opt/module"
	GenPath          = "/dagger-io"
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
	name          string
	subPath       string
	dirPath       string
	pkgName       string
	kebabName     string
	camelName     string
	version       string
	moduleVersion string
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
	if err := m.setModuleConfig(ctx, modSource, introspectionJSON); err != nil {
		return nil, err
	}
	mvnCtr, err := m.codegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	generatedCode, err := m.generateCode(ctx, mvnCtr, introspectionJSON)
	if err != nil {
		return nil, err
	}

	return dag.
		GeneratedCode(dag.Directory().WithDirectory("/", generatedCode)).
		WithVCSGeneratedPaths([]string{
			"target/generated-sources/**",
		}).
		WithVCSIgnoredPaths([]string{
			"target",
		}), nil
}

// codegenBase takes the user module code, add the generated SDK dependencies.
// If the user module code is empty, creates a default module content based on the template from the SDK
// The generated container will *not* contain the SDK source code, but only the packages built from the SDK
func (m *JavaSdk) codegenBase(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	ctr, err := m.buildJavaDependencies(ctx, introspectionJSON)
	if err != nil {
		return nil, err
	}
	ctr = ctr.
		// Copy the user module directory under /src
		WithDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		// Set the working directory to the one containing the sources to build, not just the module root
		WithWorkdir(m.moduleConfig.modulePath())
	// Add a default template if there's no existing user code
	ctr, err = m.addTemplate(ctx, ctr)
	if err != nil {
		return nil, err
	}
	ctr = ctr.
		// set the version of the Dagger dependencies to the version of the introspection file
		WithExec(m.mavenCommand(
			"mvn",
			"versions:set-property",
			"-DgenerateBackupPoms=false",
			"-Dproperty=dagger.module.deps",
			fmt.Sprintf("-DnewVersion=%s", m.moduleConfig.moduleVersion),
		))
	return ctr, nil
}

// buildJavaDependencies builds and install the needed dependencies
// used to build, package and run the user module.
// Everything will be done under ModSourceDirPath/dagger-io (m.moduleConfig.genPath()).
func (m *JavaSdk) buildJavaDependencies(
	ctx context.Context,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	// We need maven to build the dependencies
	ctr, err := m.mvnContainer(ctx)
	if err != nil {
		return nil, err
	}
	ctr = ctr.
		// Cache maven dependencies
		WithMountedCache("/root/.m2", dag.CacheVolume("sdk-java-maven-m2"), dagger.ContainerWithMountedCacheOpts{Sharing: dagger.CacheSharingModeLocked}).
		// Copy the SDK source directory, so all the files needed to build the dependencies
		WithDirectory(GenPath, m.SDKSourceDir).
		WithWorkdir(GenPath)
	// build the SDK and tools if required
	for _, project := range []string{
		"dagger-codegen-maven-plugin",
		"dagger-java-sdk",
		"dagger-java-annotation-processor",
	} {
		if exitCode, _ := ctr.WithExec(
			m.mavenCommand(
				"mvn", "-o", "dependency:get",
				fmt.Sprintf("-Dartifact=io.dagger:%s:%s", project, m.moduleConfig.version)),
			dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny}).ExitCode(ctx); exitCode != 0 {
			_, err = ctr.WithExec(m.mavenCommand(
				"mvn", "--projects", "dagger-codegen-maven-plugin", "install", "-DskipTests",
			)).ExitCode(ctx)
			if err != nil {
				return nil, err
			}
		}
	}
	return ctr.
		// Mount the introspection JSON file used to generate the SDK
		WithMountedFile("/schema.json", introspectionJSON).
		WithExec([]string{"cat", "/schema.json"}).
		// Set the version of the dependencies we are building to the version of the introspection file
		WithExec(m.mavenCommand(
			"mvn",
			"versions:set",
			"-DgenerateBackupPoms=false",
			fmt.Sprintf("-DnewVersion=%s", m.moduleConfig.moduleVersion),
		)).
		// Build and install the java modules one by one
		// - dagger-codegen-maven-plugin: this plugin will be used to generate the SDK code, from the introspection file,
		//   this means including the ability to call other projects (not part of the main dagger SDK)
		//   - this plugin is only used to build the SDK, the user module doesn't need it
		// - dagger-java-annotation-processor: this will read dagger specific annotations (@Module, @Object, @Function)
		//   and generate the entrypoint to register the module and invoke the functions
		//   - this processor will be used by the user module to generate the entrypoint, so it's referenced in the user module pom.xml
		// - dagger-java-sdk: the actual SDK, where the generated code will be written
		//   - the user module code only depends on this, it includes all the required types
		WithExec(m.mavenCommand(
			"mvn",
			"--projects", "dagger-codegen-maven-plugin,dagger-java-annotation-processor,dagger-java-sdk", "--also-make",
			"clean", "install",
			// avoid tests
			"-DskipTests",
			// specify the introspection json file
			"-Ddaggerengine.schema=/schema.json",
		)), nil
}

// addTemplate creates all the necessary files to start a new Java module
func (m *JavaSdk) addTemplate(
	ctx context.Context,
	ctr *dagger.Container,
) (*dagger.Container, error) {
	// Check if there's a pom.xml inside the module path. If a file exist, no need to add the templates
	if _, err := ctr.File(filepath.Join(m.moduleConfig.modulePath(), "pom.xml")).Name(ctx); err == nil {
		return ctr, nil
	}

	absPath := func(rel ...string) string {
		return filepath.Join(append([]string{m.moduleConfig.modulePath()}, rel...)...)
	}

	changes := []repl{
		{"dagger-module-placeholder", m.moduleConfig.kebabName},
		{"dagger-module-typedefs-placeholder", m.moduleConfig.kebabName + "-typedefs"},
		{"daggermoduleplaceholder", m.moduleConfig.pkgName},
	}

	// Edit template content so that they match the dagger module name
	templateDir := dag.CurrentModule().Source().Directory("template")
	pomXML, err := m.replace(ctx, templateDir,
		"pom.xml", changes...)
	if err != nil {
		return ctr, fmt.Errorf("could not add template: %w", err)
	}

	changes = append(changes, repl{"DaggerModule", m.moduleConfig.camelName})
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
		WithNewFile(absPath("src", "main", "java", "io", "dagger", "modules", m.moduleConfig.pkgName, fmt.Sprintf("%s.java", m.moduleConfig.camelName)), daggerModuleJava).
		WithNewFile(absPath("src", "main", "java", "io", "dagger", "modules", m.moduleConfig.pkgName, "package-info.java"), packageInfoJava)

	return ctr, nil
}

// generateCode builds and returns the generated source code and java classes
func (m *JavaSdk) generateCode(
	ctx context.Context,
	ctr *dagger.Container,
	introspectionJSON *dagger.File,
) (*dagger.Directory, error) {
	// generate the java sdk dependencies
	javaDeps, err := m.buildJavaDependencies(ctx, introspectionJSON)
	if err != nil {
		return nil, err
	}
	// generate the entrypoint class based on the user module
	entrypoint := ctr.
		// set the module name as an environment variable so we ensure constructor is only on main object
		WithEnvVariable("_DAGGER_JAVA_SDK_MODULE_NAME", m.moduleConfig.name).
		// generate the entrypoint
		WithExec(m.mavenCommand(
			"mvn",
			"clean",
			"compile",
		))
	typeDefsCtr, err := m.buildTypeDefs(ctx, ctr)
	if err != nil {
		return nil, err
	}
	return dag.
		Directory().
		// copy all user files
		WithDirectory(
			m.moduleConfig.modulePath(),
			ctr.Directory(m.moduleConfig.modulePath())).
		// copy the generated entrypoint under target/generated-sources/entrypoint
		WithDirectory(
			filepath.Join(m.moduleConfig.modulePath(), "target", "generated-sources", "entrypoint"),
			entrypoint.Directory(filepath.Join(m.moduleConfig.modulePath(), "target", "generated-sources", "annotations"))).
		// copy the generated typedefs entrypoint under target/generated-sources/typedefs
		WithDirectory(
			filepath.Join(m.moduleConfig.modulePath(), "target", "generated-sources", "typedefs"),
			typeDefsCtr.Directory(filepath.Join(m.moduleConfig.modulePath(), "target", "generated"))).
		// copy the sdk source code under target/generated-sources/dagger-io
		// this is not really generated-sources, this is the sdk. But we don't want it as the user source code
		// and we don't want to install it on the user machine. That way the java classes are made available
		// to a build system or an IDE without to interfere with the user source code
		WithDirectory(
			filepath.Join(m.moduleConfig.modulePath(), "target", "generated-sources", "dagger-io"),
			javaDeps.Directory(filepath.Join(GenPath, "dagger-java-sdk", "src", "main", "java"))).
		// copy the generated SDK files to target/generated-sources/dagger-module
		// those are all the types generated from the introspection
		WithDirectory(
			filepath.Join(m.moduleConfig.modulePath(), "target", "generated-sources", "dagger-module"),
			javaDeps.Directory(filepath.Join(GenPath, "dagger-java-sdk", "target", "generated-sources", "dagger"))).
		Directory(ModSourceDirPath), nil
}

func (m *JavaSdk) ModuleTypeDefsObject(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Module, error) {
	return dag.Module().
			WithDescription("MyJavaModule example").
			WithObject(
				dag.TypeDef().WithObject("MyJavaModule").
					WithFunction(
						dag.Function("containerEcho", dag.TypeDef().WithObject("Container")).
							WithArg("stringArg", dag.TypeDef().WithKind(dagger.TypeDefKindStringKind))).
					WithFunction(
						dag.Function("print", dag.TypeDef().WithKind(dagger.TypeDefKindBooleanKind)).
							WithArg("stringArg", dag.TypeDef().WithKind(dagger.TypeDefKindStringKind))).
					WithFunction(
						dag.Function("base", dag.TypeDef().WithObject("Container")))),
		nil
}

func (m *JavaSdk) ModuleTypeDefs(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	if err := m.setModuleConfig(ctx, modSource, introspectionJSON); err != nil {
		return nil, err
	}

	// Get a container with the user module sources and the SDK packages built and installed
	mvnCtr, err := m.codegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}
	// Build the executable jar
	jar, err := m.buildTypeDefsJar(ctx, mvnCtr)
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

func (m *JavaSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	if err := m.setModuleConfig(ctx, modSource, introspectionJSON); err != nil {
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
			WithEnvVariable("_DAGGER_JAVA_SDK_MODULE_NAME", m.moduleConfig.name).
			// build the final jar
			WithExec(m.mavenCommand(
				"mvn",
				"clean",
				"package",
				"-DskipTests",
			)),
		m.moduleConfig.modulePath())
}

func (m *JavaSdk) buildTypeDefs(
	ctx context.Context,
	ctr *dagger.Container,
) (*dagger.Container, error) {
	out, err := ctr.WithExec([]string{"find", "src/main/java", "-name", "*.java"}).Stdout(ctx)
	if err != nil {
		return nil, err
	}

	return ctr.
			WithExec(append([]string{
				"javac",
				"-cp", fmt.Sprintf("/root/.m2/repository/io/dagger/dagger-java-annotation-processor/%[1]s/dagger-java-annotation-processor-%[1]s-all.jar", m.moduleConfig.moduleVersion),
				"-processor", "io.dagger.annotation.processor.TypeDefs",
				"-proc:only",
				"-d", "target/generated",
			}, strings.Split(strings.TrimSpace(out), "\n")...)),
		nil
}

// buildTypeDefsJar builds and returns the generated jar to register types
func (m *JavaSdk) buildTypeDefsJar(
	ctx context.Context,
	ctr *dagger.Container,
) (*dagger.File, error) {
	templateDir := dag.CurrentModule().Source().Directory("template")
	typeDefsPomXML, err := m.replace(ctx, templateDir,
		"typedefs/pom.xml",
		repl{"dagger-module-typedefs-placeholder", m.moduleConfig.kebabName + "-typedefs"},
	)
	if err != nil {
		return nil, err
	}

	typeDefsCtr, err := m.buildTypeDefs(ctx, ctr)
	if err != nil {
		return nil, err
	}

	return m.finalJar(ctx,
		typeDefsCtr.
			WithWorkdir(filepath.Join(m.moduleConfig.modulePath(), "typedefs")).
			WithExec([]string{"mkdir", "-p", "src/main/java/io/dagger/gen/entrypoint"}).
			WithExec([]string{
				"cp",
				"../target/generated/io/dagger/gen/entrypoint/TypeDefs.java",
				"src/main/java/io/dagger/gen/entrypoint/TypeDefs.java",
			}).
			WithNewFile("pom.xml", typeDefsPomXML).
			WithExec(m.mavenCommand(
				"mvn",
				"versions:set-property",
				"-DgenerateBackupPoms=false",
				"-Dproperty=dagger.module.deps",
				fmt.Sprintf("-DnewVersion=%s", m.moduleConfig.moduleVersion),
			)).
			WithExec(m.mavenCommand(
				"mvn",
				"clean",
				"package",
				"-DskipTests")),
		filepath.Join(m.moduleConfig.modulePath(), "typedefs"))
}

// finalJar will return the jar corresponding to the user module built
// In order to have the final container as minimal as possible, we just want to be able to run a jar.
// To make it easy, we will rename the jar as module.jar
// But that's not easy, as we don't know the name of the built jar, so we will ask maven to give us the
// artifactId and the version so we can get the jar name
func (m *JavaSdk) finalJar(
	ctx context.Context,
	ctr *dagger.Container,
	rootDir string,
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

	return ctr.File(filepath.Join(rootDir, "target", jarFileName)), nil
}

func (m *JavaSdk) mvnContainer(ctx context.Context) (*dagger.Container, error) {
	ctr := dag.
		Container().
		From(fmt.Sprintf("%s@%s", MavenImage, MavenDigest))
	return disableSVEOnArm64(ctx, ctr)
}

func (m *JavaSdk) jreContainer(ctx context.Context) (*dagger.Container, error) {
	ctr := dag.
		Container().
		From(fmt.Sprintf("%s@%s", JavaImage, JavaDigest))
	return disableSVEOnArm64(ctx, ctr)
}

func disableSVEOnArm64(ctx context.Context, ctr *dagger.Container) (*dagger.Container, error) {
	if platform, err := ctr.Platform(ctx); err != nil {
		return nil, err
	} else if strings.Contains(string(platform), "arm64") {
		return ctr.WithEnvVariable("_JAVA_OPTIONS", "-XX:UseSVE=0"), nil
	}
	return ctr, nil
}

func (m *JavaSdk) setModuleConfig(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File) error {
	modName, err := modSource.ModuleName(ctx)
	if err != nil {
		return err
	}
	pkgName := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(modName), "-", ""), "_", "")
	kebabName := strcase.ToKebab(modName)
	camelName := strcase.ToCamel(modName)
	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return err
	}
	dirPath, err := modSource.LocalContextDirectoryPath(ctx)
	if err != nil {
		return err
	}
	version, err := m.getDaggerVersion(ctx, introspectionJSON)
	if err != nil {
		return err
	}
	moduleVersion := m.getDaggerVersionForModule(version)
	m.moduleConfig = moduleConfig{
		name:          modName,
		pkgName:       pkgName,
		kebabName:     kebabName,
		camelName:     camelName,
		subPath:       subPath,
		dirPath:       dirPath,
		version:       version,
		moduleVersion: moduleVersion,
	}

	return nil
}

func (m *JavaSdk) getDaggerVersion(ctx context.Context, introspectionJSON *dagger.File) (string, error) {
	content, err := introspectionJSON.Contents(ctx)
	if err != nil {
		return "", err
	}
	var introspectJSON IntrospectJSON
	if err = json.Unmarshal([]byte(content), &introspectJSON); err != nil {
		return "", err
	}
	return strings.TrimPrefix(introspectJSON.SchemaVersion, "v"), nil
}

func (m *JavaSdk) getDaggerVersionForModule(version string) string {
	return fmt.Sprintf(
		"%s-%s-module",
		version,
		m.moduleConfig.name,
	)
}

type IntrospectJSON struct {
	SchemaVersion string `json:"__schemaVersion"`
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
	args = append(args, "--no-transfer-progress")
	return args
}
