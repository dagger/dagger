package main

import (
	"path"
)

type GoSdk struct{}

const (
	ModMetaDirPath     = "/.daggermod"
	ModMetaInputPath   = "input.json"
	ModMetaOutputPath  = "output.json"
	ModMetaDepsDirPath = "deps"

	ModSourceDirPath      = "/src"
	runtimeExecutablePath = "/runtime"
)

type RuntimeOpts struct {
	SubPath string `doc:"sub-path of the module source to build"`
}

func (m *GoSdk) ModuleRuntime(modSource *Directory, opts RuntimeOpts) *Container {
	return m.Base().
		WithDirectory(ModSourceDirPath, modSource).
		WithWorkdir(path.Join(ModSourceDirPath, opts.SubPath)).
		WithExec([]string{"codegen", "--module", "."}, ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		WithExec([]string{
			"go", "build",
			"-o", runtimeExecutablePath,
			"-ldflags", "-s -d -w",
			".",
		}).
		WithWorkdir(ModSourceDirPath).
		WithEntrypoint([]string{runtimeExecutablePath})
}

func (m *GoSdk) Bootstrap() *Container {
	return m.ModuleRuntime(dag.Host().Directory("."), RuntimeOpts{
		SubPath: "./runtime",
	}).WithLabel("io.dagger.module.config", "/src/runtime/dagger.json")
}

// func (m *GoSdk) Codegen(modSource *Directory, opts RuntimeOpts) *Directory {
// 	return m.Base().
// 		WithMountedDirectory(ModSourceDirPath, modSource).
// 		WithWorkdir(path.Join(ModSourceDirPath, opts.SubPath)).
// 		WithExec([]string{"codegen", "--module", "."}, ContainerWithExecOpts{
// 			ExperimentalPrivilegedNesting: true,
// 		}).
// 		Directory(".").
// 		Diff(modSource.Directory(opts.SubPath))
// }

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
			"./runtime/cmd/codegen",
		}).
		File("/bin/codegen")
}
