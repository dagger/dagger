package main

import (
	"os"

	"dagger.io/dagger"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx := dagger.DefaultContext()
	ctx.Client().Environment().
		WITHCommand(Targets.Lint).
		WITHCommand(Targets.EngineLint).
		WITHCommand(Targets.Cli).
		WITHCommand(PythonTargets.PythonLint).
		WITHCommand(NodejsTargets.NodejsLint).
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
	eg.Go(func() error {
		_, err := ctx.Client().Environment().
			LoadFromUniverse("dagger/gosdk").
			Command("goLint").
			Invoke().String(ctx)
		return err
	})
	eg.Go(func() error {
		_, err := t.EngineLint(ctx)
		return err
	})
	eg.Go(func() error {
		_, err := PythonTargets{t}.PythonLint(ctx)
		return err
	})
	eg.Go(func() error {
		_, err := NodejsTargets{t}.NodejsLint(ctx)
		return err
	})
	return "", eg.Wait()
}
