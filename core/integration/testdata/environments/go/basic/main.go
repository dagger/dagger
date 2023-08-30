package main

import (
	"context"
)

func main() {
	dag.CurrentEnvironment().
		WithCheck(CoolStaticCheck).
		WithCheck(SadStaticCheck).
		WithCheck(CoolContainerCheck).
		WithCheck(SadContainerCheck).
		Serve()
}

func CoolStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(true, StaticCheckResultOpts{
		Output: "My cool check is cool",
	}), nil
}

func SadStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(false, StaticCheckResultOpts{
		Output: "My sad check is sad",
	}), nil
}

func CoolContainerCheck(ctx context.Context) (*Check, error) {
	return dag.Check().WithContainer(dag.Container().From("alpine:3.18").WithExec([]string{"true"})), nil
}

func SadContainerCheck(ctx context.Context) (*Check, error) {
	return dag.Check().WithContainer(dag.Container().From("alpine:3.18").WithExec([]string{"false"})), nil
}
