package main

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

const (
	javaSDKPath = "sdk/java"
	mavenImage  = "maven:3.9.9-eclipse-temurin-21-alpine"
	mavenDigest = "sha256:4cbb8bf76c46b97e028998f2486ed014759a8e932480431039bdb93dffe6813e"
)

type JavaSDK struct {
	Dagger *DaggerDev // +private
}

func (t JavaSDK) Name() string {
	return "java"
}

// Lint the Java SDK
func (t JavaSDK) Lint(ctx context.Context) (MyCheckStatus, error) {
	_, err := t.Maven(ctx).
		WithExec([]string{"mvn", "fmt:check"}).
		Sync(ctx)
	return CheckCompleted, err
}

// Test the Java SDK
func (t JavaSDK) Test(ctx context.Context) (MyCheckStatus, error) {
	_, err := t.Maven(ctx).
		With(t.Dagger.devEngineSidecar()).
		WithExec([]string{"mvn", "clean", "verify", "-Ddaggerengine.version=local"}).
		Sync(ctx)
	if err != nil {
		return CheckCompleted, err
	}
	return CheckCompleted, nil
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

	_, err := t.Maven(ctx).
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{"apt-get", "-y", "install", "gpg"}).
		WithExec([]string{"mvn", "versions:set", fmt.Sprintf("-DnewVersion=%s", version)}).
		WithExec([]string{"mvn", "clean", "deploy", "-Prelease", fmt.Sprintf("-Dmaven.deploy.skip=%s", skipDeploy)}).
		WithExec([]string{"find", ".", "-name", "*.jar"}).
		Sync(ctx)
	return err
}

var stableVersionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Bump the Java SDK's Engine dependency
func (t JavaSDK) Bump(ctx context.Context, version string) (*dagger.Changeset, error) {
	version = strings.TrimPrefix(version, "v")
	v := version
	if !stableVersionRe.MatchString(v) {
		v = fmt.Sprintf("%s-SNAPSHOT", v)
	}
	bumpCtr := t.Maven(ctx).
		WithExec([]string{
			"mvn",
			"versions:set",
			"-DgenerateBackupPoms=false",
			"-DnewVersion=" + v,
		}).
		WithExec([]string{
			"mvn",
			"versions:set-property",
			"-DgenerateBackupPoms=false",
			"-Dproperty=daggerengine.version",
			"-DnewVersion=" + version,
		}).
		WithWorkdir("runtime/template").
		WithExec([]string{
			"mvn",
			"versions:set-property",
			"-DgenerateBackupPoms=false",
			"-Dproperty=dagger.module.deps",
			"-DnewVersion=" + version + "-template-module",
		})

	poms := []string{
		"/sdk/java/dagger-codegen-maven-plugin/pom.xml",
		"/sdk/java/dagger-java-annotation-processor/pom.xml",
		"/sdk/java/dagger-java-sdk/pom.xml",
		"/sdk/java/dagger-java-samples/pom.xml",
		"/sdk/java/pom.xml",
		"/sdk/java/runtime/template/pom.xml",
	}

	bumped := dag.Directory()
	for _, pom := range poms {
		bumped = bumped.WithFile(pom, bumpCtr.File(pom))
	}
	return bumped.
		Changes(dag.Directory()).
		Sync(ctx)
}

// Bump dependencies in the Java SDK
func (t JavaSDK) BumpDeps(ctx context.Context) *dagger.Changeset {
	poms := []string{
		"/sdk/java/dagger-codegen-maven-plugin/pom.xml",
		"/sdk/java/dagger-java-annotation-processor/pom.xml",
		"/sdk/java/dagger-java-sdk/pom.xml",
		"/sdk/java/dagger-java-samples/pom.xml",
		"/sdk/java/pom.xml",
	}
	bumpCtr := t.Maven(ctx)
	for _, pom := range poms {
		bumpCtr = bumpCtr.
			WithWorkdir(path.Dir(pom)).
			WithExec([]string{"mvn", "versions:update-properties"})
	}
	bumped := dag.Directory()
	for _, pom := range poms {
		bumped = bumped.WithFile(pom, bumpCtr.File(pom))
	}
	return bumped.Changes(dag.Directory())
}

func (t JavaSDK) Maven(ctx context.Context) *dagger.Container {
	src := t.Dagger.Source.Directory(javaSDKPath)
	mountPath := "/" + javaSDKPath

	return dag.Container().
		From(fmt.Sprintf("%s@%s", mavenImage, mavenDigest)).
		WithMountedCache("/root/.m2", dag.CacheVolume("sdk-java-maven-m2")).
		With(func(ctr *dagger.Container) *dagger.Container {
			if platform, err := ctr.Platform(ctx); err == nil && strings.Contains(string(platform), "arm64") {
				ctr = ctr.WithEnvVariable("_JAVA_OPTIONS", "-XX:UseSVE=0")
			}
			return ctr
		}).
		WithWorkdir(mountPath).
		WithDirectory(mountPath, src)
}
