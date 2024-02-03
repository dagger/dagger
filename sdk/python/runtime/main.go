package main

import (
	"context"
	_ "embed"
	"fmt"
	"path"
)

func New(
	// +optional
	sdkSourceDir *Directory,
) *PythonSdk {
	return &PythonSdk{
		SDKSourceDir: sdkSourceDir,
		RequiredPaths: []string{
			"**/pyproject.toml",
		},
	}
}

type PythonSdk struct {
	SDKSourceDir  *Directory
	RequiredPaths []string
}

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

func (m *PythonSdk) ModuleRuntime(
	ctx context.Context,
	modSource *ModuleSource,
	introspectionJson string,
) (*Container, error) {
	ctr, err := m.CodegenBase(ctx, modSource, introspectionJson)
	if err != nil {
		return nil, err
	}

	return ctr.
		WithExec([]string{"python", "-m", "pip", "install", "."}).
		WithNewFile(RuntimeExecutablePath, ContainerWithNewFileOpts{
			Contents:    runtimeTmpl,
			Permissions: 0755,
		}).
		WithEntrypoint([]string{RuntimeExecutablePath}), nil
}

func (m *PythonSdk) Codegen(ctx context.Context, modSource *ModuleSource, introspectionJson string) (*GeneratedCode, error) {
	ctr, err := m.CodegenBase(ctx, modSource, introspectionJson)
	if err != nil {
		return nil, err
	}

	ctr = ctr.WithDirectory(genDir, ctr.Directory(sdkSrc), ContainerWithDirectoryOpts{
		Exclude: []string{
			"**/__pycache__",
		},
	})

	return dag.GeneratedCode(ctr.Directory(ModSourceDirPath)).
		WithVCSGeneratedPaths(
			[]string{genDir + "/**"},
		).
		WithVCSIgnoredPaths(
			[]string{genDir},
		), nil
}

func (m *PythonSdk) CodegenBase(ctx context.Context, modSource *ModuleSource, introspectionJson string) (*Container, error) {
	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %v", err)
	}

	return m.Base("").
		WithMountedDirectory(sdkSrc, m.SDKSourceDir.WithoutDirectory("runtime")).
		WithMountedDirectory("/opt", dag.CurrentModule().Source().Directory("./template")).
		WithExec([]string{"python", "-m", "pip", "install", "-e", sdkSrc}).
		WithMountedDirectory(ModSourceDirPath, modSource.BaseContextDirectory()).
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
		WithExec([]string{"sh", "-c", "[ -f pyproject.toml ] || cp /opt/pyproject.toml ."}).
		WithExec([]string{"sh", "-c", "find . -name '*.py' | grep -q . || { mkdir -p src; cp /opt/src/main.py src/main.py; }"}), nil
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
