package main

import (
	"context"
	"dagger/internal/dagger"
	"fmt"
	"regexp"
	"strings"
)

const (
	javaSDKPath             = "sdk/java"
	javaSDKVersionPomPath   = javaSDKPath + "/pom.xml"
	javaSchemasDirPath      = javaSDKPath + "/dagger-codegen-maven-plugin/src/main/resources/schemas"
	javaGeneratedSchemaPath = "target/generated-schema/schema.json"
	javaVersion             = "17"
	mavenVersion            = "3.9"
)

type JavaSDK struct {
	Dagger *Dagger // +private
}

// Lint lints the Java SDK
func (t JavaSDK) Lint(ctx context.Context) error {
	_, err := t.javaBase().
		WithExec([]string{"mvn", "fmt:check"}).
		Sync(ctx)
	return err
}

// Test tests the Java SDK
func (t JavaSDK) Test(ctx context.Context) error {
	ctr, err := t.Dagger.installDagger(ctx, t.javaBase(), "sdk-java-test")
	if err != nil {
		return err
	}

	_, err = ctr.
		WithExec([]string{"mvn", "clean", "verify", "-Ddaggerengine.version=local"}).
		Sync(ctx)
	return err
}

// Generate re-generates the Java SDK API
func (t JavaSDK) Generate(ctx context.Context) (*Directory, error) {
	ctr, err := t.Dagger.installDagger(ctx, t.javaBase(), "sdk-java-generate")
	if err != nil {
		return nil, err
	}

	generatedSchema, err := ctr.
		WithExec([]string{"mvn", "clean", "install", "-pl", "dagger-codegen-maven-plugin"}).
		WithExec([]string{"mvn", "-N", "dagger-codegen:generateSchema"}).
		File(javaGeneratedSchemaPath).
		Contents(ctx)
	if err != nil {
		return nil, err
	}

	engineVersion, err := ctr.
		WithExec([]string{"dagger", "version"}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	engineVersion = strings.TrimPrefix(strings.Fields(engineVersion)[1], "v")

	dir := dag.Directory().WithNewFile(javaSchemasDirPath+fmt.Sprintf("/schema-%s.json", engineVersion), generatedSchema)
	return dir, nil
}

// Publish publishes the Java SDK
func (t JavaSDK) Publish(
	ctx context.Context,
	tag string,

	// +optional
	dryRun bool,
) error {
	version := strings.TrimPrefix(tag, "sdk/java/v")

	skipDeploy := "true" // FIXME: Always set to true as long as the maven central deployment is not configured
	if dryRun {
		skipDeploy = "true"
	}

	_, err := t.javaBase().
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{"apt-get", "-y", "install", "gpg"}).
		WithExec([]string{"mvn", "versions:set", fmt.Sprintf("-DnewVersion=%s", version)}).
		WithExec([]string{"mvn", "clean", "deploy", "-Prelease", fmt.Sprintf("-Dmaven.deploy.skip=%s", skipDeploy)}).
		WithExec([]string{"find", ".", "-name", "*.jar"}).
		Sync(ctx)
	return err
}

// Bump the Java SDK's Engine dependency
func (t JavaSDK) Bump(ctx context.Context, version string) (*Directory, error) {
	contents, err := t.Dagger.Source.File(javaSDKVersionPomPath).Contents(ctx)
	if err != nil {
		return nil, err
	}

	newVersion := fmt.Sprintf(`<daggerengine.version>%s</daggerengine.version>`, strings.TrimPrefix(version, "v"))

	versionRe, err := regexp.Compile(`<daggerengine\.version>([0-9\.\-a-zA-Z]+)<\/daggerengine\.version>`)
	if err != nil {
		return nil, err
	}
	newContents := versionRe.ReplaceAllString(contents, newVersion)

	dir := dag.Directory().WithNewFile(javaSDKVersionPomPath, newContents)
	return dir, nil
}

func (t JavaSDK) javaBase() *dagger.Container {
	src := t.Dagger.Source.Directory(javaSDKPath)
	mountPath := "/" + javaSDKPath

	return dag.Container().
		From(fmt.Sprintf("maven:%s-eclipse-temurin-%s", mavenVersion, javaVersion)).
		WithWorkdir(mountPath).
		WithDirectory(mountPath, src).
		WithMountedCache("/root/.m2", dag.CacheVolume("maven-cache"))
}
