package main

import (
	"context"
	"dagger/dev/internal/dagger"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	MavenImage  = "maven:3.9.9-eclipse-temurin-21-alpine"
	MavenDigest = "sha256:4cbb8bf76c46b97e028998f2486ed014759a8e932480431039bdb93dffe6813e"

	SourceDirPath = "/src"
	SdkDirPath    = SourceDirPath + "/sdk/java"
)

// The Java SDK's development module
type Dev struct {
	Container         *dagger.Container
	MavenErrors       bool
	MavenDebugLogging bool
}

func New(
	ctx context.Context,
	// Java SDK source directory
	// +defaultPath="/sdk/java"
	// +ignore=["**", "!dagger-codegen-maven-plugin/", "!dagger-java-annotation-processor/", "!dagger-java-sdk/", "!dagger-java-samples/pom.xml", "!LICENSE", "!README.md", "!pom.xml", "**/src/test", "**/target"]
	source *dagger.Directory,
	// Enable maven errors
	// +optional
	// +default=false
	mavenErrors bool,
	// Enable full debug logging for maven
	// +optional
	// +default=false
	mavenDebugLogging bool,
) (*Dev, error) {
	ctr, err := mvnContainer(ctx)
	if err != nil {
		return nil, err
	}
	return &Dev{
		Container: ctr.
			WithDirectory(SdkDirPath, source).
			WithWorkdir(SdkDirPath),
		MavenErrors:       mavenErrors,
		MavenDebugLogging: mavenDebugLogging,
	}, nil
}

// Generate the SDK files based on the given introspection file
func (m *Dev) Generate(
	// Introspection file describing the schema to generate types
	introspectionJSON *dagger.File,
) *Dev {
	m.Container = m.Container.
		// ensure the codegen plugin is built
		WithExec(m.mavenCommand("mvn", "clean", "install", "-pl", "dagger-codegen-maven-plugin")).
		// mount schema file from codegen
		WithMountedFile(filepath.Join(SourceDirPath, "schema.json"), introspectionJSON).
		// generate sdk files
		WithExec(m.mavenCommand(
			"mvn", "compile",
			"-pl", "dagger-java-sdk", "--also-make",
			"-Ddaggerengine.schema="+filepath.Join(SourceDirPath, "schema.json")))
	return m
}

// Return all generated files for the Java SDK
func (m *Dev) GeneratedSources() *dagger.Directory {
	generatedSources := filepath.Join("dagger-java-sdk", "target", "generated-sources", "dagger")
	return dag.Directory().
		WithDirectory(
			generatedSources,
			m.Container.Directory(filepath.Join(SdkDirPath, generatedSources)))
}

func (m *Dev) mavenCommand(args ...string) []string {
	if m.MavenErrors {
		args = append(args, "-e")
	}
	if m.MavenDebugLogging {
		args = append(args, "-X")
	}
	return args
}

func mvnContainer(ctx context.Context) (*dagger.Container, error) {
	ctr := dag.
		Container().
		From(fmt.Sprintf("%s@%s", MavenImage, MavenDigest))
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
