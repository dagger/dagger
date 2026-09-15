package main

import (
	"context"
	"sort"
	"strings"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context) (string, error) {
	deps, err := dag.CurrentModule().Dependencies(ctx)
	if err != nil {
		return "", err
	}

	var depNames []string
	for _, dep := range deps {
		depName, err := dep.Name(ctx)
		if err != nil {
			return "", err
		}

		depNames = append(depNames, depName)
	}

	sort.Strings(depNames)

	return strings.Join(depNames, ","), nil
}
