package core

import "github.com/dagger/dagger/dagql"

// immutableInputDependencies adapts legacy ownership hooks whose returned
// dependencies are all stored, fixed recipe references.
func immutableInputDependencies(results []dagql.AnyResult, err error) ([]dagql.DependencyResult, error) {
	if err != nil || results == nil {
		return nil, err
	}
	deps := make([]dagql.DependencyResult, len(results))
	for i, result := range results {
		deps[i] = dagql.DependencyResult{Result: result, Owned: true, ImmutableInput: true}
	}
	return deps, nil
}

func dependencyResultValues(deps []dagql.DependencyResult, err error) ([]dagql.AnyResult, error) {
	if err != nil || deps == nil {
		return nil, err
	}
	results := make([]dagql.AnyResult, len(deps))
	for i, dep := range deps {
		results[i] = dep.Result
	}
	return results, nil
}
