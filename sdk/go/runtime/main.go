package main

import (
	"path"
	"path/filepath"
)

type GoSdk struct{}

const (
	ModSourceDirPath      = "/src"
	RuntimeExecutablePath = "/runtime"
)

type RuntimeOpts struct {
	SubPath  string   `doc:"Sub-path of the source directory that contains the module config."`
	Platform Platform `doc:"Platform to build for."`
}

func (m *GoSdk) ModuleRuntime(modSource *Directory, opts RuntimeOpts) *Container {
	modSubPath := filepath.Join(ModSourceDirPath, opts.SubPath)
	return m.Base(opts.Platform).
		WithDirectory(ModSourceDirPath, modSource).
		WithWorkdir(modSubPath).
		WithExec([]string{"codegen", "--module", "."}, ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		WithExec([]string{
			"go", "build",
			"-o", RuntimeExecutablePath,
			"-ldflags", "-s -d -w",
			".",
		}).
		WithWorkdir(ModSourceDirPath).
		WithEntrypoint([]string{RuntimeExecutablePath}).
		WithLabel("io.dagger.module.config", modSubPath)
}

func (m *GoSdk) Codegen(modSource *Directory, opts RuntimeOpts) *GeneratedCode {
	base := m.Base(opts.Platform).
		WithMountedDirectory(ModSourceDirPath, modSource).
		WithWorkdir(path.Join(ModSourceDirPath, opts.SubPath))

	codegen := base.
		WithExec([]string{"codegen", "--module", ".", "--propagate-logs"}, ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Directory(".")

	return dag.GeneratedCode().
		WithCode(base.Directory(".").Diff(codegen)).
		WithVCSIgnoredPaths([]string{
			"dagger.gen.go",
			"internal/querybuilder/",
			"querybuilder/", // for old repos
		})
}

func (m *GoSdk) Base(platform Platform) *Container {
	return m.goBase(platform).
		WithFile("/usr/bin/codegen", m.CodegenBin(platform))
}

func (m *GoSdk) CodegenBin(platform Platform) *File {
	return m.goBase(platform).
		WithMountedDirectory("/sdk", dag.Host().Directory(".")).
		WithExec([]string{
			"go", "build",
			"-C", "/sdk",
			"-o", "/bin/codegen",
			"./cmd/codegen",
		}).
		File("/bin/codegen")
}

func (m *GoSdk) goBase(platform Platform) *Container {
	opts := ContainerOpts{}
	if platform != "" {
		opts.Platform = platform
	}
	return dag.Container(opts).
		From("golang:1.21-alpine").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("modgomodcache")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("modgobuildcache"))
}
