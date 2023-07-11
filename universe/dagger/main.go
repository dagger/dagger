package main

import (
	"errors"
	"os"
	"strings"

	"dagger.io/dagger"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx := dagger.DefaultContext()
	ctx.Client().Environment().
		WithCheck_(Targets.Lint).
		WithCheck_(Targets.EngineLint).
		WithCommand_(Targets.Cli).
		WithCheck_(PythonTargets.PythonLint).
		WithCheck_(NodejsTargets.NodejsLint).
		// Merge in all the entrypoints from the Go SDK too under the "go" namespace
		// TODO:
		// WithExtension(ctx.Client().Environment().LoadFromUniverse("dagger/gosdk"), "go").
		Serve(ctx)
}

type Targets struct {
	// If set, the git repo to pull the dagger repo source code from
	Repo string
	// If set, the branch of the --repo setting
	Branch string
}

func (t Targets) srcDir(ctx dagger.Context) *dagger.Directory {
	// TODO: shouldn't be needed
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	srcDir := ctx.Client().Host().Directory(cwd)
	if t.Repo != "" {
		srcDir = ctx.Client().Git(t.Repo).Branch(t.Branch).Tree()
	}
	return srcDir
}

// Lint everything (engine, sdks, etc)
func (t Targets) Lint(ctx dagger.Context) (string, error) {
	var eg errgroup.Group

	// TODO: these should be subchecks

	var goSdkOutput string
	var goSdkErr error
	eg.Go(func() error {
		goSdkOutput, goSdkErr = ctx.Client().Environment().
			LoadFromUniverse("dagger/gosdk").
			Check("goLint").
			Result().Output(ctx)
		return goSdkErr
	})

	var engineOutput string
	var engineErr error
	eg.Go(func() error {
		engineOutput, engineErr = t.EngineLint(ctx)
		return engineErr
	})

	var pythonOutput string
	var pythonErr error
	eg.Go(func() error {
		pythonOutput, pythonErr = PythonTargets{t}.PythonLint(ctx)
		return pythonErr
	})

	var nodejsOutput string
	var nodejsErr error
	eg.Go(func() error {
		nodejsOutput, nodejsErr = NodejsTargets{t}.NodejsLint(ctx)
		return nodejsErr
	})

	err := eg.Wait()
	if err != nil {
		return "", errors.Join(goSdkErr, engineErr, pythonErr, nodejsErr)
	}
	return strings.Join([]string{goSdkOutput, engineOutput, pythonOutput, nodejsOutput}, "\n"), nil
}
