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

func (m *GoSdk) Bootstrap() *Container {
	return m.ModuleRuntime(dag.Host().Directory("."), RuntimeOpts{
		SubPath: "./runtime",
	})
}

func (m *GoSdk) Codegen(modSource *Directory, opts RuntimeOpts) *Directory {
	base := m.Base(opts.Platform).
		WithMountedDirectory(ModSourceDirPath, modSource).
		WithWorkdir(path.Join(ModSourceDirPath, opts.SubPath))

	return base.Directory(".").Diff(
		base.
			WithExec([]string{"codegen", "--module", ".", "--vcs", "--propagate-logs"}, ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			}).
			Directory("."),
	)
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
