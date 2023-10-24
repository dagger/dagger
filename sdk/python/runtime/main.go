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

var srcMainTmpl = `from dagger.ext import function


@function
def hello() -> str:
    """Returns a friendly greeting"""
    return "Hello, world!"
`

var runtimeTmpl = `#!/usr/bin/env python
import sys
from dagger.ext.cli import app
if __name__ == '__main__':
    sys.exit(app())
`

func (m *PythonSdk) ModuleRuntime(modSource *Directory, subPath string) *Container {
    return m.CodegenBase(modSource, subPath).
        WithExec([]string{"python", "-m", "pip", "install", "."}).
		WithWorkdir(ModSourceDirPath).
        WithNewFile(RuntimeExecutablePath, ContainerWithNewFileOpts{
            Contents: runtimeTmpl,
            Permissions:     0755,
        }).
        WithEntrypoint([]string{RuntimeExecutablePath}).
        WithDefaultArgs()
}

func (m *PythonSdk) Codegen(modSource *Directory, subPath string) *GeneratedCode {
    ctr := m.CodegenBase(modSource, subPath)
    ctr = ctr.WithDirectory(genDir, ctr.Directory(sdkSrc), ContainerWithDirectoryOpts{
        Exclude: []string{
            "**/_pycache_",
        },
    })

    modified := ctr.Directory(ModSourceDirPath)
    diff := modSource.Diff(modified)

	return dag.GeneratedCode(diff).
        WithVCSIgnoredPaths([]string{
            genDir,
        })
}

func (m *PythonSdk) CodegenBase(modSource *Directory, subPath string) *Container {
    return m.Base("").
        WithMountedDirectory(ModSourceDirPath, modSource).
        WithWorkdir(path.Join(ModSourceDirPath, subPath)).
        // Move all of this to a python script.
        WithNewFile("/templates/pyproject.toml", ContainerWithNewFileOpts{
            Contents: pyprojectTmpl,
        }).
        WithNewFile("/templates/src/main.py", ContainerWithNewFileOpts{
            Contents: srcMainTmpl,
        }).
        WithExec([]string{"python", "-m", "dagger", "generate", path.Join(sdkSrc, genPath)}, ContainerWithExecOpts{
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
