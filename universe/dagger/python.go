package main

import (
	"fmt"

	"dagger.io/dagger"
	"golang.org/x/sync/errgroup"
)

const (
	pythonPath    = "/root/.local/bin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	venv          = "/opt/venv"
	pythonAppDir  = "sdk/python"
	pythonVersion = "3.11"
	reqFile       = "requirements.txt"
)

func pythonSDKSrc(ctx dagger.Context) *dagger.Directory {
	return srcDir(ctx).Directory(pythonAppDir)
}

func pythonBase(ctx dagger.Context) *dagger.Container {
	pipx := ctx.Client().HTTP("https://github.com/pypa/pipx/releases/download/1.2.0/pipx.pyz")
	src := pythonSDKSrc(ctx)

	// Mirror the same dir structure from the repo because of the
	// relative paths in ruff (for docs linting).
	mountPath := fmt.Sprintf("/%s", pythonAppDir)
	reqPath := fmt.Sprintf("%s/%s", pythonAppDir, reqFile)

	return ctx.Client().Container().
		From(fmt.Sprintf("python:%s-slim", pythonVersion)).
		WithEnvVariable("PIPX_BIN_DIR", "/usr/local/bin").
		WithMountedCache("/root/.cache/pip", ctx.Client().CacheVolume("pip_cache")).
		WithMountedCache("/root/.local/pipx/cache", ctx.Client().CacheVolume("pipx_cache")).
		WithMountedCache("/root/.cache/hatch", ctx.Client().CacheVolume("hatch_cache")).
		WithMountedFile("/pipx.pyz", pipx).
		WithExec([]string{"python", "/pipx.pyz", "install", "hatch==1.7.0"}).
		WithExec([]string{"python", "-m", "venv", venv}).
		WithEnvVariable("VIRTUAL_ENV", venv).
		WithEnvVariable(
			"PATH",
			"$VIRTUAL_ENV/bin:$PATH",
			dagger.ContainerWithEnvVariableOpts{
				Expand: true,
			},
		).
		WithEnvVariable("HATCH_ENV_TYPE_VIRTUAL_PATH", venv).
		WithFile(reqPath, src.File(reqFile)).
		WithExec([]string{"pip", "install", "-r", reqPath}).
		WithDirectory(mountPath, src).
		WithWorkdir(mountPath).
		WithExec([]string{"pip", "install", ".[cli]"})
}

// Lint the Dagger Python SDK
func PythonLint(ctx dagger.Context) (string, error) {
	base := pythonBase(ctx)
	eg, gctx := errgroup.WithContext(ctx)
	var output string
	eg.Go(func() error {
		path := "docs/current"
		var err error
		output, err = base.
			WithDirectory(
				fmt.Sprintf("/%s", path),
				srcDir(ctx).Directory(path),
				dagger.ContainerWithDirectoryOpts{
					Include: []string{
						"**/*.py",
						".ruff.toml",
					},
				},
			).
			WithExec([]string{"hatch", "run", "lint"}).
			Stderr(gctx)
		return err
	})

	// TODO: test generated code too

	return output, eg.Wait()
}
