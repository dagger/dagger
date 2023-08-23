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
		WithCheck(CoolCompositeCheck).
		WithCheck(SadCompositeCheck).
		WithCheck(dag.BasicExplicitdep().AnotherCoolStaticCheck()).
		WithCheck(dag.BasicExplicitdep().AnotherSadStaticCheck()).
		WithCheck(CoolCompositeCheckFromExplicitDep).
		WithCheck(SadCompositeCheckFromExplicitDep).
		WithCheck(CoolCompositeCheckFromDynamicDep).
		WithCheck(SadCompositeCheckFromDynamicDep).
		Serve()
}

func CoolStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(true, StaticCheckResultOpts{
		Output: "We're cool",
	}), nil
}

func SadStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(false, StaticCheckResultOpts{
		Output: "So sad...",
	}), nil
}

func CoolContainerCheck(ctx context.Context) (*Check, error) {
	return dag.Check().WithContainer(dag.Container().From("alpine:3.18").WithExec([]string{"true"})), nil
}

func SadContainerCheck(ctx context.Context) (*Check, error) {
	return dag.Check().WithContainer(dag.Container().From("alpine:3.18").WithExec([]string{"false"})), nil
}

func CoolCompositeCheck(ctx context.Context) (*Check, error) {
	ctr := dag.Container().From("alpine:3.18")
	return dag.Check().
		WithSubcheck(dag.Check().WithName("CoolSubcheck1").WithContainer(
			ctr.WithEnvVariable("BASIC", "1").WithExec([]string{"sh", "-c", "echo BASIC $BASIC; true"}),
		)).WithSubcheck(dag.Check().WithName("CoolSubcheck2").WithContainer(
		ctr.WithEnvVariable("BASIC", "2").WithExec([]string{"sh", "-c", "echo BASIC $BASIC; true"}),
	)), nil
}

func SadCompositeCheck(ctx context.Context) (*Check, error) {
	ctr := dag.Container().From("alpine:3.18")
	return dag.Check().
		WithSubcheck(dag.Check().WithName("SadSubcheck3").WithContainer(
			ctr.WithEnvVariable("BASIC", "3").WithExec([]string{"sh", "-c", "echo BASIC $BASIC; false"}),
		)).WithSubcheck(dag.Check().WithName("SadSubcheck4").WithContainer(
		ctr.WithEnvVariable("BASIC", "4").WithExec([]string{"sh", "-c", "echo BASIC $BASIC; false"}),
	)), nil
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
	// TODO: also cover dynamically adding a check to this environment and calling that
	dynamicDep := dag.Environment().Load(
		dag.Host().Directory("."),
		"./core/integration/testdata/environments/go/basic/dynamicdep",
	)

	return dag.Check().
		WithSubcheck(dynamicDep.Check("yetAnotherCoolStaticCheck")).
		WithSubcheck(dynamicDep.Check("yetAnotherCoolContainerCheck")).
		WithSubcheck(dynamicDep.Check("yetAnotherCoolCompositeCheck")), nil
}

func SadCompositeCheckFromDynamicDep(ctx context.Context) (*Check, error) {
	dynamicDep := dag.Environment().Load(
		dag.Host().Directory("."),
		"./core/integration/testdata/environments/go/basic/dynamicdep",
	)

	return dag.Check().
		WithSubcheck(dynamicDep.Check("yetAnotherSadStaticCheck")).
		WithSubcheck(dynamicDep.Check("yetAnotherSadContainerCheck")).
		WithSubcheck(dynamicDep.Check("yetAnotherSadCompositeCheck")), nil
}
