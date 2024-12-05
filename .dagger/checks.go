package main

import (
	"context"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
)

const (
	CheckDocs          = "docs"
	CheckGoSDK         = "sdk/go"
	CheckPythonSDK     = "sdk/python"
	CheckTypescriptSDK = "sdk/typescript"
	CheckPHPSDK        = "sdk/php"
	CheckJavaSDK       = "sdk/java"
	CheckRustSDK       = "sdk/rust"
	CheckElixirSDK     = "sdk/elixir"
	CheckRubySDK       = "sdk/ruby"
)

// Check that everything works. Use this as CI entrypoint.
func (dev *DaggerDev) Check(ctx context.Context,
	// Directories to check
	// +optional
	targets []string,
) error {
	var routes checkRouter
	routes.Add(Check{"docs", (&Docs{Dagger: dev}).Lint})
	routes.Add(dev.checksForSDK(CheckGoSDK, dev.SDK().Go)...)
	routes.Add(dev.checksForSDK(CheckPythonSDK, dev.SDK().Python)...)
	routes.Add(dev.checksForSDK(CheckTypescriptSDK, dev.SDK().Typescript)...)
	routes.Add(dev.checksForSDK(CheckPHPSDK, dev.SDK().PHP)...)
	routes.Add(dev.checksForSDK(CheckJavaSDK, dev.SDK().Java)...)
	routes.Add(dev.checksForSDK(CheckRustSDK, dev.SDK().Rust)...)
	routes.Add(dev.checksForSDK(CheckElixirSDK, dev.SDK().Elixir)...)
	routes.Add(dev.checksForSDK(CheckRubySDK, dev.SDK().Ruby)...)

	eg, ctx := errgroup.WithContext(ctx)
	for _, check := range routes.Get(targets...) {
		ctx, span := Tracer().Start(ctx, check.Name)
		eg.Go(func() (rerr error) {
			defer func() {
				if rerr != nil {
					span.SetStatus(codes.Error, rerr.Error())
				}
				span.End()
			}()
			return check.Check(ctx)
		})
	}
	return eg.Wait()
}

type Check struct {
	Name  string
	Check func(context.Context) error
}

func (dev *DaggerDev) checksForSDK(name string, sdk sdkBase) []Check {
	return []Check{
		{
			Name:  name + "/lint",
			Check: sdk.Lint,
		},
		{
			Name:  name + "/test",
			Check: sdk.Test,
		},
		{
			Name: name + "/test-publish",
			Check: func(ctx context.Context) error {
				branches, err := dev.Git.Branches(ctx, dagger.VersionGitBranchesOpts{
					Commit: "HEAD",
				})
				if err != nil {
					return err
				}
				var name string
				if len(branches) == 0 {
					name = "HEAD"
				} else {
					name, err = branches[0].Branch(ctx)
					if err != nil {
						return err
					}
				}
				return sdk.TestPublish(ctx, name)
			},
		},
	}
}

// checkRouter allows easily storing and fetching checks
// It's similar in style to go-test, where specifying a prefix will match all children.
type checkRouter struct {
	check    Check
	children map[string]*checkRouter
}

func (r *checkRouter) Add(checks ...Check) {
	for _, check := range checks {
		r.add(check.Name, check)
	}
}

func (r *checkRouter) Get(targets ...string) []Check {
	var checks []Check
	for _, target := range targets {
		checks = append(checks, r.get(target).all()...)
	}
	return checks
}

func (r *checkRouter) add(target string, check Check) {
	if target == "" {
		r.check = check
		return
	}

	target, rest, _ := strings.Cut(target, "/")
	if r.children == nil {
		r.children = make(map[string]*checkRouter)
	}
	if _, ok := r.children[target]; !ok {
		r.children[target] = &checkRouter{}
	}
	r.children[target].add(rest, check)
}

func (r *checkRouter) get(target string) *checkRouter {
	if r == nil {
		return nil
	}
	if target == "" {
		return r
	}

	target, rest, _ := strings.Cut(target, "/")
	if r.children == nil {
		return nil
	}
	if _, ok := r.children[target]; !ok {
		return nil
	}
	return r.children[target].get(rest)
}

func (r *checkRouter) all() []Check {
	if r == nil {
		return nil
	}
	var checks []Check
	if r.check.Check != nil {
		checks = append(checks, r.check)
	}
	for _, child := range r.children {
		checks = append(checks, child.all()...)
	}
	return checks
}
