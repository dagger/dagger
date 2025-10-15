package main

import (
	"context"
	"path"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
)

// Check that everything works. Use this as CI entrypoint.
func (dev *DaggerDev) Check(ctx context.Context,
	// Directories to check
	// +optional
	targets []string,
) error {
	var routes checkRouter
	routes.Add(Check{"docs", dag.Docs().Lint})
	routes.Add(Check{"scripts/lint", dev.Scripts().Lint})
	routes.Add(Check{"scripts/test", dev.Scripts().Test})
	routes.Add(Check{"helm/lint", dag.Helm().Lint})
	routes.Add(Check{"helm/test", dag.Helm().Test})
	routes.Add(Check{"helm/test-publish", func(ctx context.Context) error {
		return dag.Helm().Publish(ctx, "main", dagger.HelmPublishOpts{DryRun: true})
	}})
	routes.Add(dev.checksForSDK("sdk/go", dev.SDK().Go)...)
	routes.Add(dev.checksForSDK("sdk/python", dev.SDK().Python)...)
	routes.Add(dev.checksForSDK("sdk/typescript", dev.SDK().Typescript)...)
	routes.Add(dev.checksForSDK("sdk/php", dev.SDK().PHP)...)
	routes.Add(dev.checksForSDK("sdk/java", dev.SDK().Java)...)
	routes.Add(dev.checksForSDK("sdk/rust", dev.SDK().Rust)...)
	routes.Add(dev.checksForSDK("sdk/elixir", dev.SDK().Elixir)...)
	routes.Add(dev.checksForSDK("sdk/dotnet", dev.SDK().Dotnet)...)

	if len(targets) == 0 {
		for route := range routes.children {
			targets = append(targets, route)
		}
	}
	eg := errgroup.Group{}
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
				return sdk.TestPublish(ctx, "HEAD")
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
		checks = append(checks, r.get(target)...)
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

func (r *checkRouter) get(target string) []Check {
	if r == nil {
		return nil
	}
	if target == "" {
		return r.all()
	}

	target, rest, _ := strings.Cut(target, "/")
	if r.children == nil {
		return nil
	}
	var results []Check
	for k, v := range r.children {
		if ok, _ := path.Match(target, k); ok {
			results = append(results, v.get(rest)...)
		}
	}
	return results
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
