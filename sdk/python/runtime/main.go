package main

import (
	"os"
	"path"
	"path/filepath"
)

type PythonSdk struct{}

const (
	ModSourceDirPath      = "/src"
	RuntimeExecutablePath = "/runtime"
)

func (m *PythonSdk) ModuleRuntime(modSource *Directory, subPath string) *Container {
	modSubPath := filepath.Join(ModSourceDirPath, subPath)
	return m.Base().
		WithDirectory(ModSourceDirPath, modSource).
		WithWorkdir(modSubPath).
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
		WithWorkdir(modSubPath).
		WithDefaultArgs().
		WithEntrypoint([]string{RuntimeExecutablePath})
}

func (m *PythonSdk) Codegen(modSource *Directory, subPath string) *GeneratedCode {
	base := m.Base().
		WithMountedDirectory(ModSourceDirPath, modSource).
		WithWorkdir(path.Join(ModSourceDirPath, subPath))

	codegen := base.
		WithExec([]string{"codegen", "generate", "/sdk/src/dagger/client/gen.py"}, ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Directory("/sdk")

	return dag.GeneratedCode(dag.Directory().WithDirectory("sdk", codegen)).
		WithVCSIgnoredPaths([]string{
			"sdk",
		})
}

func (m *PythonSdk) Base() *Container {
	return m.pyBase().
		WithDirectory("/sdk", dag.Host().Directory(root())).
		WithFile("/usr/bin/codegen", m.CodegenBin())
}

func (m *PythonSdk) CodegenBin() *File {
	return m.pyBase().
		WithMountedDirectory("/sdk", dag.Host().Directory(root())).
		WithExec([]string{
			"shiv",
			"-e", "dagger:_codegen.cli:main",
			"-o", "/bin/codegen",
			"--root", "/tmp/.shiv",
			"/sdk",
		}).
		File("/bin/codegen")
}

func (m *PythonSdk) pyBase() *Container {
	return dag.Container().
		From("python:3.11-alpine").
		WithExec([]string{"apk", "add", "--no-cache", "git"}).
		WithMountedCache("/root/.cache/pip", dag.CacheVolume("modpythonpipcache")).
		WithExec([]string{"pip", "install", "shiv"})
}

// TODO: fix .. restriction
func root() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Join(wd, "..")
}
