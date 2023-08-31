package main

import (
	"context"
	"fmt"

	"github.com/iancoleman/strcase"
)

func checkTests(env *Environment) *Environment {
	return env.
		WithCheck(CoolStaticCheck).
		WithCheck(SadStaticCheck).
		WithCheck(CoolContainerCheck).
		WithCheck(SadContainerCheck).
		WithCheck(CoolCompositeCheck).
		WithCheck(SadCompositeCheck).
		WithCheck(dag.BasicExplicitdep().AnotherCoolStaticCheck()).
		WithCheck(dag.BasicExplicitdep().AnotherSadStaticCheck()).
		WithCheck(CoolCompositeCheckFromExplicitDep).
		WithCheck(SadCompositeCheckFromExplicitDep).
		WithCheck(CoolCompositeCheckFromDynamicDep).
		WithCheck(SadCompositeCheckFromDynamicDep).
		WithCheck(CoolCheckOnlyReturn).
		WithCheck(CoolCheckResultOnlyReturn).
		WithCheck(CoolStringOnlyReturn).
		WithCheck(CoolErrorOnlyReturn).
		WithCheck(SadErrorOnlyReturn).
		WithCheck(CoolStringErrorReturn).
		WithCheck(SadStringErrorReturn)
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

func CoolStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(true, StaticCheckResultOpts{
		Output: checkOutput("CoolStaticCheck"),
	}), nil
}

func SadStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(false, StaticCheckResultOpts{
		Output: checkOutput("SadStaticCheck"),
	}), nil
}

func CoolContainerCheck(ctx context.Context) (*Check, error) {
	return containerCheck("CoolContainerCheck", true), nil
}

func SadContainerCheck(ctx context.Context) (*Check, error) {
	return containerCheck("SadContainerCheck", false), nil
}

func CoolCompositeCheck(ctx context.Context) (*Check, error) {
	return dag.Check().
		WithSubcheck(containerCheck("CoolSubcheckA", true)).
		WithSubcheck(containerCheck("CoolSubcheckB", true)), nil
}

func SadCompositeCheck(ctx context.Context) (*Check, error) {
	return dag.Check().
		WithSubcheck(containerCheck("SadSubcheckA", false)).
		WithSubcheck(containerCheck("SadSubcheckB", false)), nil
}

func CoolCompositeCheckFromExplicitDep(ctx context.Context) (*Check, error) {
	return dag.Check().
		WithSubcheck(dag.BasicExplicitdep().AnotherCoolStaticCheck()).
		WithSubcheck(dag.BasicExplicitdep().AnotherCoolContainerCheck()).
		WithSubcheck(dag.BasicExplicitdep().AnotherCoolCompositeCheck()), nil
}

func SadCompositeCheckFromExplicitDep(ctx context.Context) (*Check, error) {
	return dag.Check().
		WithSubcheck(dag.BasicExplicitdep().AnotherSadStaticCheck()).
		WithSubcheck(dag.BasicExplicitdep().AnotherSadContainerCheck()).
		WithSubcheck(dag.BasicExplicitdep().AnotherSadCompositeCheck()), nil
}

func CoolCompositeCheckFromDynamicDep(ctx context.Context) (*Check, error) {
	dynamicDep := dag.Environment().FromConfig(dag.Host().Directory("."), EnvironmentFromConfigOpts{
		ConfigPath: "./core/integration/testdata/environments/go/basic/dynamicdep",
	})

	return dag.Check().
		WithSubcheck(dynamicDep.Check("yetAnotherCoolStaticCheck")).
		WithSubcheck(dynamicDep.Check("yetAnotherCoolContainerCheck")).
		WithSubcheck(dynamicDep.Check("yetAnotherCoolCompositeCheck")), nil
}

func SadCompositeCheckFromDynamicDep(ctx context.Context) (*Check, error) {
	dynamicDep := dag.Environment().FromConfig(dag.Host().Directory("."), EnvironmentFromConfigOpts{
		ConfigPath: "./core/integration/testdata/environments/go/basic/dynamicdep",
	})

	return dag.Check().
		WithSubcheck(dynamicDep.Check("yetAnotherSadStaticCheck")).
		WithSubcheck(dynamicDep.Check("yetAnotherSadContainerCheck")).
		WithSubcheck(dynamicDep.Check("yetAnotherSadCompositeCheck")), nil
}

func CoolCheckOnlyReturn(ctx context.Context) *Check {
	return containerCheck("CoolCheckOnlyReturn", true)
}

func CoolCheckResultOnlyReturn(ctx context.Context) *CheckResult {
	return dag.StaticCheckResult(true, StaticCheckResultOpts{
		Output: checkOutput("CoolCheckResultOnlyReturn"),
	})
}

func CoolStringOnlyReturn(ctx context.Context) string {
	return checkOutput("CoolStringOnlyReturn")
}

func CoolErrorOnlyReturn(ctx context.Context) error {
	return nil
}

func SadErrorOnlyReturn(ctx context.Context) error {
	return fmt.Errorf(checkOutput("SadErrorOnlyReturn"))
}

func CoolStringErrorReturn(ctx context.Context) (string, error) {
	return checkOutput("CoolStringErrorReturn"), nil
}

func SadStringErrorReturn(ctx context.Context) (string, error) {
	return "", fmt.Errorf(checkOutput("SadStringErrorReturn"))
}
