package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/test/internal/dagger"
)

type Test struct{}

// Check passes objects from the dependency as arguments more than once and
// reports, for each case, whether both uses saw the same file.
// +cache="never"
func (m *Test) Check(ctx context.Context) (string, error) {
	var lines []string
	report := func(name, a, b string) {
		verdict := "different"
		if a == b {
			verdict = "same"
		}
		lines = append(lines, name+" "+verdict)
	}

	f := dag.Dep().Never()
	a, b, err := oneQuery(ctx, f)
	if err != nil {
		return "", err
	}
	report("never-one-query", a, b)

	f = dag.Dep().Never()
	if a, err = read(ctx, f); err != nil {
		return "", err
	}
	if b, err = read(ctx, f); err != nil {
		return "", err
	}
	report("never-two-queries", a, b)

	if a, b, err = oneQuery(ctx, dag.Dep().Never().WithName("derived")); err != nil {
		return "", err
	}
	report("never-derived", a, b)

	ctr := base().WithFile("/x", dag.Dep().Never())
	if a, err = ctr.File("/x").Contents(ctx); err != nil {
		return "", err
	}
	if b, err = ctr.File("/x").Contents(ctx); err != nil {
		return "", err
	}
	report("never-built-once", a, b)

	if a, b, err = oneQuery(ctx, dag.Dep().Cached()); err != nil {
		return "", err
	}
	report("cached-one-query", a, b)

	return strings.Join(lines, "\n"), nil
}

func base() *dagger.Container {
	return dag.Container().From("alpine:3.22.1")
}

// oneQuery passes f twice in one query and returns what each use saw.
func oneQuery(ctx context.Context, f *dagger.File) (string, string, error) {
	out, err := base().WithFile("/a", f).WithFile("/b", f).
		WithExec([]string{"sh", "-c", "cat /a; echo; cat /b"}).Stdout(ctx)
	if err != nil {
		return "", "", err
	}
	a, b, ok := strings.Cut(strings.TrimSpace(out), "\n")
	if !ok {
		return "", "", fmt.Errorf("unexpected output %q", out)
	}
	return a, b, nil
}

// read passes f in its own query and returns what that use saw.
func read(ctx context.Context, f *dagger.File) (string, error) {
	return base().WithFile("/x", f).File("/x").Contents(ctx)
}
