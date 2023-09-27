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
	SubPath string `doc:"Sub-path of the source directory that contains the module config."`
}

func (m *GoSdk) ModuleRuntime(modSource *Directory, opts RuntimeOpts) *Container {
	modSubPath := filepath.Join(ModSourceDirPath, opts.SubPath)
	return m.Base().
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

func (m *GoSdk) Bootstrap() *Container {
	return m.ModuleRuntime(dag.Host().Directory("."), RuntimeOpts{
		SubPath: "./runtime",
	})
}

func (m *GoSdk) Codegen(modSource *Directory, opts RuntimeOpts) *Directory {
	base := m.Base().
		WithMountedDirectory(ModSourceDirPath, modSource).
		WithWorkdir(path.Join(ModSourceDirPath, opts.SubPath))
	return base.Directory(".").Diff(
		base.
			WithExec([]string{"codegen", "--module", ".", "--vcs"}, ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			}).
			Directory("."),
	)
}

func (m *GoSdk) Base() *Container {
	return m.goBase().
		WithFile("/usr/bin/codegen", m.CodegenBin())
}

func (m *GoSdk) goBase() *Container {
	return dag.Container().
		From("golang:1.21-alpine").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("modgomodcache")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("modgobuildcache"))
}

func (m *GoSdk) CodegenBin() *File {
	return m.goBase().
		WithMountedDirectory("/sdk", dag.Host().Directory(".")).
		WithExec([]string{
			"go", "build",
			"-C", "/sdk",
			"-o", "/bin/codegen",
			"./cmd/codegen",
		}).
		File("/bin/codegen")
}
