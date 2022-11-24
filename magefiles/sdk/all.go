package sdk

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"github.com/magefile/mage/mg"
	"golang.org/x/sync/errgroup"
)

type SDK interface {
	Lint(ctx context.Context) error
	Test(ctx context.Context) error
	Generate(ctx context.Context) error
	Publish(ctx context.Context, tag string) error
	Bump(ctx context.Context, engineVersion string) error
}

var availableSDKs = []SDK{
	&Go{},
	&Python{},
	&Nodejs{},
}

var _ SDK = All{}

type All mg.Namespace

// Lint runs all SDK linters
func (t All) Lint(ctx context.Context) error {
	return t.runAll(ctx, func(ctx context.Context, s SDK) error {
		return s.Lint(ctx)
	})
}

// Test runs all SDK tests
func (t All) Test(ctx context.Context) error {
	return t.runAll(ctx, func(ctx context.Context, s SDK) error {
		return s.Test(ctx)
	})
}

// Generate re-generates all SDK APIs
func (t All) Generate(ctx context.Context) error {
	return t.runAll(ctx, func(ctx context.Context, s SDK) error {
		return s.Generate(ctx)
	})
}

// Publish publishes all SDK APIs
func (t All) Publish(ctx context.Context, version string) error {
	return errors.New("publish is not supported on `all` target. Please run this command on individual SDKs")
}

// Bump SDKs to a specific Engine version
func (t All) Bump(ctx context.Context, engineVersion string) error {
	return t.runAll(ctx, func(ctx context.Context, s SDK) error {
		return s.Bump(ctx, engineVersion)
	})
}

func (t All) runAll(ctx context.Context, fn func(context.Context, SDK) error) error {
	eg, gctx := errgroup.WithContext(ctx)

	for _, sdk := range availableSDKs {
		sdk := sdk
		eg.Go(func() error {
			return fn(gctx, sdk)
		})
	}
	return eg.Wait()
}

// lintGeneratedCode ensures the generated code is up to date.
//
// 1) Read currently generated code
// 2) Generate again
// 3) Compare
// 4) Restore original generated code.
func lintGeneratedCode(fn func() error, files ...string) error {
	originals := map[string][]byte{}
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		originals[f] = content
	}

	defer func() {
		for _, f := range files {
			defer os.WriteFile(f, originals[f], 0600)
		}
	}()

	if err := fn(); err != nil {
		return err
	}

	for _, f := range files {
		original := string(originals[f])
		updated, err := os.ReadFile(f)
		if err != nil {
			return err
		}

		if original != string(updated) {
			edits := myers.ComputeEdits(span.URIFromPath(f), original, string(updated))
			diff := fmt.Sprint(gotextdiff.ToUnified(f, f, original, edits))
			return fmt.Errorf("generated api mismatch. please run `mage sdk:all:generate`:\n%s", diff)
		}
	}

	return nil
}
