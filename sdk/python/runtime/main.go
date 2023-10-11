package main

import (
	"path"
	"path/filepath"
)

type PythonSdk struct{}

const (
	ModSourceDirPath      = "/src"
	RuntimeExecutablePath = "/runtime"
)

type RuntimeOpts struct {
	SubPath  string   `doc:"Sub-path of the source directory that contains the module config."`
	Platform Platform `doc:"Platform to build for."`
}

func (m *PythonSdk) ModuleRuntime(modSource *Directory, opts RuntimeOpts) *Container {
	modSubPath := filepath.Join(ModSourceDirPath, opts.SubPath)
	return m.Base(opts.Platform).
		WithDirectory(ModSourceDirPath, modSource).
		WithWorkdir(modSubPath).
		WithExec([]string{"sh", "-c", "ls -lha"}).
		WithExec([]string{"codegen", "generate", "/sdk/src/dagger/client/gen.py"}, ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		WithExec([]string{
			"shiv",
			"-e", "dagger.ext.cli:app",
			"-o", RuntimeExecutablePath,
			"--root", "/tmp/.shiv",
			"/sdk",
			".",
		}).
		WithWorkdir(ModSourceDirPath).
		WithDefaultArgs().
		WithEntrypoint([]string{RuntimeExecutablePath}).
		WithLabel("io.dagger.module.config", modSubPath)
}

func (m *PythonSdk) Codegen(modSource *Directory, opts RuntimeOpts) *GeneratedCode {
	base := m.Base(opts.Platform).
		WithMountedDirectory(ModSourceDirPath, modSource).
		WithWorkdir(path.Join(ModSourceDirPath, opts.SubPath))

	codegen := base.
		WithExec([]string{"codegen", "generate", "/sdk/src/dagger/client/gen.py"}, ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Directory("/sdk")

	return dag.GeneratedCode().
		WithCode(dag.Directory().WithDirectory("sdk", codegen)).
		WithVCSIgnoredPaths([]string{
			"sdk",
		})
}

func (m *PythonSdk) Base(platform Platform) *Container {
	return m.pyBase(platform).
		WithDirectory("/sdk", dag.Host().Directory(".")).
		WithFile("/usr/bin/codegen", m.CodegenBin(platform))
}

func (m *PythonSdk) CodegenBin(platform Platform) *File {
	return m.pyBase(platform).
		WithMountedDirectory("/sdk", dag.Host().Directory(".")).
		WithExec([]string{
			"shiv",
			"-e", "dagger:_codegen.cli:main",
			"-o", "/bin/codegen",
			"--root", "/tmp/.shiv",
			"/sdk",
		}).
		File("/bin/codegen")
}

func (m *PythonSdk) pyBase(platform Platform) *Container {
	opts := ContainerOpts{}
	if platform != "" {
		opts.Platform = platform
	}
	return dag.Container(opts).
		From("python:3.11-alpine").
		WithExec([]string{"apk", "add", "--no-cache", "git"}).
		WithMountedCache("/root/.cache/pip", dag.CacheVolume("modpythonpipcache")).
		WithExec([]string{"pip", "install", "shiv"})
}
