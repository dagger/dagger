package main

import (
	_ "embed"
	"os"
	"path"
	"path/filepath"
)

type PythonSdk struct{}

const (
	ModSourceDirPath      = "/src"
	RuntimeExecutablePath = "/runtime"
	venv                  = "/opt/venv"
	sdkSrc                = "/sdk"
	genDir                = "sdk"
	genPath               = "src/dagger/client/gen.py"
	schemaPath            = "/schema.json"
	defaultPythonVersion  = "3.11-slim"
	defaultPythonDigest   = "sha256:8f64a67710f3d981cf3008d6f9f1dbe61accd7927f165f4e37ea3f8b883ccc3f"
)

//go:embed scripts/runtime.py
var runtimeTmpl string

func (m *PythonSdk) ModuleRuntime(modSource *Directory, subPath string, introspectionJson string) *Container {
	return m.CodegenBase(modSource, subPath, introspectionJson).
		WithExec([]string{"python", "-m", "pip", "install", "."}).
		WithWorkdir(ModSourceDirPath).
		WithNewFile(RuntimeExecutablePath, ContainerWithNewFileOpts{
			Contents:    runtimeTmpl,
			Permissions: 0755,
		}).
		WithEntrypoint([]string{RuntimeExecutablePath})
}

func (m *PythonSdk) Codegen(modSource *Directory, subPath string, introspectionJson string) *GeneratedCode {
	ctr := m.CodegenBase(modSource, subPath, introspectionJson)
	ctr = ctr.WithDirectory(genDir, ctr.Directory(sdkSrc), ContainerWithDirectoryOpts{
		Exclude: []string{
			"**/__pycache__",
		},
	})

	modified := ctr.Directory(ModSourceDirPath)
	diff := modSource.Diff(modified).Directory(subPath)

	return dag.GeneratedCode(diff).
		WithVCSIgnoredPaths([]string{
			genDir,
		})
}

func (m *PythonSdk) CodegenBase(modSource *Directory, subPath string, introspectionJson string) *Container {
	return m.Base("").
		WithDirectory(sdkSrc, dag.Host().Directory(root(), HostDirectoryOpts{
			Exclude: []string{"runtime"},
		})).
		WithMountedDirectory("/opt", dag.Host().Directory(root(), HostDirectoryOpts{
			Include: []string{"runtime/template"},
		})).
		WithExec([]string{"python", "-m", "pip", "install", "-e", sdkSrc}).
		WithMountedDirectory(ModSourceDirPath, modSource).
		WithWorkdir(path.Join(ModSourceDirPath, subPath)).
		WithNewFile(schemaPath, ContainerWithNewFileOpts{
			Contents: introspectionJson,
		}).
		// TODO: Move all of this to a python script, add more intelligence.
		WithExec([]string{
			"python", "-m", "dagger", "codegen",
			"--output", path.Join(sdkSrc, genPath),
			"--introspection", schemaPath,
		}, ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		WithExec([]string{"sh", "-c", "[ -f pyproject.toml ] || cp /opt/runtime/template/pyproject.toml ."}).
		WithExec([]string{"sh", "-c", "find . -name '*.py' | grep -q . || { mkdir -p src; cp /opt/runtime/template/src/main.py src/main.py; }"})
}

func (m *PythonSdk) Base(version string) *Container {
	if version == "" {
		version = defaultPythonVersion + "@" + defaultPythonDigest
	}
	return dag.Container().
		From("python:"+version).
		WithMountedCache("/root/.cache/pip", dag.CacheVolume("modpipcache-"+version)).
		WithExec([]string{"python", "-m", "venv", venv}).
		WithEnvVariable("VIRTUAL_ENV", venv).
		WithEnvVariable("PATH", "$VIRTUAL_ENV/bin:$PATH", ContainerWithEnvVariableOpts{
			Expand: true,
		})
}

// TODO: fix .. restriction
func root() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Join(wd, "..")
}
