package main

import (
	"context"
	"strings"
)

func main() {
	dag.CurrentEnvironment().
		With(checkTests).
		With(currentEnvTests).
		Serve()
}

func currentEnvTests(env *Environment) *Environment {
	return env.
		WithCheck(CurrentEnvironmentCheck)
}

func CurrentEnvironmentCheck(ctx context.Context) (*CheckResult, error) {
	env := dag.CurrentEnvironment()

	envName, err := env.Name(ctx)
	if err != nil {
		return nil, err
	}
	if envName != "basic" {
		return dag.StaticCheckResult(false, StaticCheckResultOpts{
			Output: "expected env name to be 'basic', got " + envName,
		}), nil
	}

	checks, err := env.Checks(ctx)
	if err != nil {
		return nil, err
	}
	expectedCheckNames := map[string]struct{}{
		"coolStaticCheck":                   {},
		"sadStaticCheck":                    {},
		"coolContainerCheck":                {},
		"sadContainerCheck":                 {},
		"coolCompositeCheck":                {},
		"sadCompositeCheck":                 {},
		"anotherCoolStaticCheck":            {},
		"anotherSadStaticCheck":             {},
		"coolCompositeCheckFromExplicitDep": {},
		"sadCompositeCheckFromExplicitDep":  {},
		"coolCompositeCheckFromDynamicDep":  {},
		"sadCompositeCheckFromDynamicDep":   {},
	}
	for _, check := range checks {
		check := check
		id, err := check.ID(ctx)
		if err != nil {
			return nil, err
		}
		check = *dag.Check(CheckOpts{ID: id})
		checkName, err := check.Name(ctx)
		if err != nil {
			return nil, err
		}
		delete(expectedCheckNames, checkName)
	}
	if len(expectedCheckNames) > 0 {
		var missing []string
		for name := range expectedCheckNames {
			missing = append(missing, name)
		}
		return dag.StaticCheckResult(false, StaticCheckResultOpts{
			Output: "expected checks not found: " + strings.Join(missing, ", "),
		}), nil
	}
	return dag.StaticCheckResult(true), nil
}
