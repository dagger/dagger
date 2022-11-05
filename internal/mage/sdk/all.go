package sdk

import (
	"context"
	"fmt"
	"os"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"github.com/magefile/mage/mg"
)

type SDK interface {
	Lint(context.Context) error
	Test(context.Context) error
	Generate(context.Context) error
}

var availableSDKs = []SDK{
	&Go{},
	&Python{},
}

var _ SDK = All{}

type All mg.Namespace

// Lint runs all SDK linters
func (t All) Lint(ctx context.Context) error {
	return t.runAll(func(s SDK) any {
		return s.Lint
	})
}

// Test runs all SDK tests
func (t All) Test(ctx context.Context) error {
	return t.runAll(func(s SDK) any {
		return s.Test
	})
}

// Generate re-generates all SDK APIs
func (t All) Generate(ctx context.Context) error {
	return t.runAll(func(s SDK) any {
		return s.Generate
	})
}

func (t All) runAll(fn func(SDK) any) error {
	handlers := []any{}
	for _, sdk := range availableSDKs {
		handlers = append(handlers, fn(sdk))
	}
	mg.Deps(handlers...)
	return nil
}

// lintGeneratedCode ensures the generated code is up to date.
//
// 1) Read currently generated code
// 2) Generate again
// 3) Compare
// 4) Restore original generated code.
func lintGeneratedCode(sdkPath string, fn func() error) error {
	original, err := os.ReadFile(sdkPath)
	if err != nil {
		return err
	}
	defer os.WriteFile(sdkPath, original, 0600)

	if err := fn(); err != nil {
		return err
	}
	new, err := os.ReadFile(sdkPath)
	if err != nil {
		return err
	}

	// diff := cmp.Diff(string(original), string(new))
	if string(original) != string(new) {
		edits := myers.ComputeEdits(span.URIFromPath(sdkPath), string(original), string(new))
		diff := fmt.Sprint(gotextdiff.ToUnified(sdkPath, sdkPath, string(original), edits))
		return fmt.Errorf("generated api mismatch. please run `mage sdk:all:generate`:\n%s", diff)
	}

	return err
}
