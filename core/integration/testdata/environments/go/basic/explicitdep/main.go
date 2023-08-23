package main

import (
	"context"

	"github.com/ettle/strcase"
)

func main() {
	dag.CurrentEnvironment().
		WithCheck(AnotherCoolStaticCheck).
		WithCheck(AnotherSadStaticCheck).
		WithCheck(AnotherCoolContainerCheck).
		WithCheck(AnotherSadContainerCheck).
		WithCheck(AnotherCoolCompositeCheck).
		WithCheck(AnotherSadCompositeCheck).
		Serve()
}

func checkOutput(name string) string {
	return "WE ARE RUNNING CHECK " + strcase.ToKebab(name)
}

func containerCheck(name string, succeed bool) *Check {
	cmd := "false"
	if succeed {
		cmd = "true"
	}
	ctr := dag.Container().From("alpine:3.18").
		WithExec([]string{"sh", "-e", "-c", "echo " + checkOutput(name) + "; " + cmd})
	return dag.Check().WithName(name).WithContainer(ctr)
}

func AnotherCoolStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(true, StaticCheckResultOpts{
		Output: checkOutput("AnotherCoolStaticCheck"),
	}), nil
}

func AnotherSadStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(false, StaticCheckResultOpts{
		Output: checkOutput("AnotherSadStaticCheck"),
	}), nil
}

func AnotherCoolContainerCheck(ctx context.Context) (*Check, error) {
	return containerCheck("AnotherCoolContainerCheck", true), nil
}

func AnotherSadContainerCheck(ctx context.Context) (*Check, error) {
	return containerCheck("AnotherSadContainerCheck", false), nil
}

func AnotherCoolCompositeCheck(ctx context.Context) (*Check, error) {
	return dag.Check().
		WithSubcheck(containerCheck("AnotherCoolSubcheck1", true)).
		WithSubcheck(containerCheck("AnotherCoolSubcheck2", true)), nil
}

func AnotherSadCompositeCheck(ctx context.Context) (*Check, error) {
	return dag.Check().
		WithSubcheck(containerCheck("AnotherSadSubcheck1", false)).
		WithSubcheck(containerCheck("AnotherSadSubcheck2", false)), nil
}
