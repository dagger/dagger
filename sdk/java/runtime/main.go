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
	JavaImage   = "eclipse-temurin:23-jdk-alpine"
	JavaDigest  = "sha256:2cb34eaa1bb3d1bdcd6c82c020216385ca278db70311682d8c173f320ee4f4c4"

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
	// +defaultPath="/sdk/java"
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
		//GeneratedCode(mvnCtr.Directory(ModSourceDirPath)).
		WithVCSGeneratedPaths([]string{
			"target",
		}).
		WithVCSIgnoredPaths([]string{
			"target",
		}), nil

	//return dag.GeneratedCode(dag.Directory()), nil
}

func (m *JavaSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	if err := m.setModuleConfig(ctx, modSource); err != nil {
		return nil, err
	}
	mvnCtr, err := m.codegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	mvnCtr = mvnCtr.
		WithWorkdir(m.moduleConfig.modulePath()).
		WithExec([]string{"mvn", "clean", "compile"}).
		WithExec([]string{"cat", "target/classes/dagger_module_info.json"}).
		WithExec([]string{"mvn", "package", "-DskipTests"})

	jar := mvnCtr.
		File(filepath.Join(m.moduleConfig.modulePath(), "target", "dagger-java-module-1.0-SNAPSHOT.jar"))

	javaCtr := dag.Container().
		From(fmt.Sprintf("%s@%s", JavaImage, JavaDigest)).
		WithFile(filepath.Join(ModDirPath, "module.jar"), jar).
		WithWorkdir(ModDirPath).
		WithEntrypoint([]string{"java", "-XX:UseSVE=0", "-jar", filepath.Join(ModDirPath, "module.jar")})

	return javaCtr, nil
}

func (m *JavaSdk) codegenBase(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File) (*dagger.Container, error) {
	if err := m.setModuleConfig(ctx, modSource); err != nil {
		return nil, err
	}

	mvn := dag.Container().
		From(fmt.Sprintf("%s@%s", MavenImage, MavenDigest))

	ctr := mvn.
		WithDirectory(ModSourceDirPath, m.SDKSourceDir).
		WithWorkdir(ModSourceDirPath)
	if introspectionJSON != nil {
		ctr = ctr.WithMountedFile("/schema.json", introspectionJSON)
	}

	sdkDir := ctr.
		Directory(".")

	ctxDir := modSource.ContextDirectory()

	ctr = ctr.
		WithMountedDirectory(ModSourceDirPath, ctxDir).
		WithDirectory(m.moduleConfig.sdkPath(), sdkDir).
		WithWorkdir(m.moduleConfig.sdkPath()).
		WithExec([]string{"mvn", "clean", "install", "-DskipTests", "-Ddaggerengine.schema=/schema.json"})

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
	m.moduleConfig = moduleConfig{
		name:    modName,
		subPath: subPath,
	}

	return nil
}
