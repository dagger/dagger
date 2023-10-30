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
	sdkSrc                = "/sdk"
	venv                  = "/opt/venv"
	genDir                = "sdk"
	genPath               = "src/dagger/client/gen.py"
)

var pyprojectTmpl = `[project]
name = "main"
version = "0.0.0"
`

var srcMainTmpl = `import dagger
from dagger.mod import function


@function
def container_echo(string_arg: str) -> dagger.Container:
    # Example usage: "dagger call container-echo --string-arg hello"
    return dagger.container().from_("alpine:latest").with_exec(["echo", string_arg])


@function
async def grep_dir(directory_arg: dagger.Directory, pattern: str) -> str:
    # Example usage: "dagger call grep-dir --directory-arg . --patern grep_dir"
    return await (
        dagger.container()
        .from_("alpine:latest")
        .with_mounted_directory("/mnt", directory_arg)
        .with_workdir("/mnt")
        .with_exec(["grep", "-R", pattern, "."])
        .stdout()
    )
`

var runtimeTmpl = `#!/usr/bin/env python
import sys
from dagger.mod.cli import app
if __name__ == '__main__':
    sys.exit(app())
`

func (m *PythonSdk) ModuleRuntime(modSource *Directory, subPath string, introspectionJson string) *Container {
	return m.CodegenBase(modSource, subPath, introspectionJson).
		WithExec([]string{"python", "-m", "pip", "install", "."}).
		WithWorkdir(ModSourceDirPath).
		WithNewFile(RuntimeExecutablePath, ContainerWithNewFileOpts{
			Contents:    runtimeTmpl,
			Permissions: 0755,
		}).
		WithEntrypoint([]string{RuntimeExecutablePath}).
		WithDefaultArgs()
}

func (m *PythonSdk) Codegen(modSource *Directory, subPath string, introspectionJson string) *GeneratedCode {
	ctr := m.CodegenBase(modSource, subPath, introspectionJson)
	ctr = ctr.WithDirectory(genDir, ctr.Directory(sdkSrc), ContainerWithDirectoryOpts{
		Exclude: []string{
			"**/__pycache__",
		},
	})

	modified := ctr.Directory(ModSourceDirPath)
	diff := modSource.Diff(modified)

	return dag.GeneratedCode(diff).
		WithVCSIgnoredPaths([]string{
			genDir,
		})
}

func (m *PythonSdk) CodegenBase(modSource *Directory, subPath string, introspectionJson string) *Container {
	return m.Base("").
		WithMountedDirectory(ModSourceDirPath, modSource).
		WithWorkdir(path.Join(ModSourceDirPath, subPath)).
		// TODO: Move all of this to a python script.
		WithNewFile("/templates/pyproject.toml", ContainerWithNewFileOpts{
			Contents: pyprojectTmpl,
		}).
		WithNewFile("/templates/src/main.py", ContainerWithNewFileOpts{
			Contents: srcMainTmpl,
		}).
		WithNewFile("/schema.json", ContainerWithNewFileOpts{
			Contents: introspectionJson,
		}).
		WithExec([]string{
			"python", "-m", "dagger", "codegen",
			"--output", path.Join(sdkSrc, genPath),
			"--introspection", "/schema.json",
		}, ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		WithExec([]string{"sh", "-c", "[ -f pyproject.toml ] || cp /templates/pyproject.toml ."}).
		WithExec([]string{"sh", "-c", "find . -name '*.py' | grep -q . || { mkdir -p src; cp /templates/src/main.py src/main.py; }"})
}

func (m *PythonSdk) Base(version string) *Container {
	if version == "" {
		version = "3.11-slim"
	}
	return dag.Container().
		From("python:"+version).
		WithMountedCache("/root/.cache/pip", dag.CacheVolume("modpipcache-"+version)).
		WithExec([]string{"python", "-m", "venv", venv}).
		WithEnvVariable("VIRTUAL_ENV", venv).
		WithEnvVariable("PATH", "$VIRTUAL_ENV/bin:$PATH", ContainerWithEnvVariableOpts{
			Expand: true,
		}).
		WithDirectory(sdkSrc, dag.Host().Directory(root(), HostDirectoryOpts{
			Exclude: []string{"runtime"},
		})).
		WithExec([]string{"python", "-m", "pip", "install", "-e", sdkSrc})
}

// TODO: fix .. restriction
func root() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Join(wd, "..")
}
