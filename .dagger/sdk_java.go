package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
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
	Dagger *DaggerDev // +private
}

// Lint the Java SDK
func (t JavaSDK) Lint(ctx context.Context) error {
	_, err := t.javaBase().
		WithExec([]string{"mvn", "fmt:check"}).
		Sync(ctx)
	return err
}

// Test the Java SDK
func (t JavaSDK) Test(ctx context.Context) error {
	installer, err := t.Dagger.installer(ctx, "sdk")
	if err != nil {
		return err
	}

	_, err = t.javaBase().
		With(installer).
		WithExec([]string{"mvn", "clean", "verify", "-Ddaggerengine.version=local"}).
		Sync(ctx)
	return err
}

// Regenerate the Java SDK API
func (t JavaSDK) Generate(ctx context.Context) (*dagger.Directory, error) {
	installer, err := t.Dagger.installer(ctx, "sdk")
	if err != nil {
		return nil, err
	}

	base := t.javaBase().With(installer)

	generatedSchema, err := base.
		WithExec([]string{"mvn", "clean", "install", "-pl", "dagger-codegen-maven-plugin"}).
		WithExec([]string{"mvn", "-N", "dagger-codegen:generateSchema"}).
		File(javaGeneratedSchemaPath).
		Contents(ctx)
	if err != nil {
		return nil, err
	}

	engineVersion, err := base.
		WithExec([]string{"dagger", "version"}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	engineVersion = strings.TrimPrefix(strings.Fields(engineVersion)[1], "v")

	dir := dag.Directory().WithNewFile(javaSchemasDirPath+fmt.Sprintf("/schema-%s.json", engineVersion), generatedSchema)
	return dir, nil
}

// Test the publishing process
func (t JavaSDK) TestPublish(ctx context.Context, tag string) error {
	// FIXME: we don't have a working test-publish implementation
	return nil
}

// Publish the Java SDK
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

var javaVersionRe = regexp.MustCompile(`<daggerengine\.version>([0-9\.\-a-zA-Z]+)<\/daggerengine\.version>`)

// Bump the Java SDK's Engine dependency
func (t JavaSDK) Bump(ctx context.Context, version string) (*dagger.Directory, error) {
	contents, err := t.Dagger.Source().File(javaSDKVersionPomPath).Contents(ctx)
	if err != nil {
		return nil, err
	}

	newVersion := fmt.Sprintf(`<daggerengine.version>%s</daggerengine.version>`, strings.TrimPrefix(version, "v"))
	newContents := javaVersionRe.ReplaceAllString(contents, newVersion)

	dir := dag.Directory().WithNewFile(javaSDKVersionPomPath, newContents)
	return dir, nil
}

func (t JavaSDK) javaBase() *dagger.Container {
	src := t.Dagger.Source().Directory(javaSDKPath)
	mountPath := "/" + javaSDKPath

	return dag.Container().
		From(fmt.Sprintf("maven:%s-eclipse-temurin-%s", mavenVersion, javaVersion)).
		WithWorkdir(mountPath).
		WithDirectory(mountPath, src).
		WithMountedCache("/root/.m2", dag.CacheVolume("maven-cache"))
}
