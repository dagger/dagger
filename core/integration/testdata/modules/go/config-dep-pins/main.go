package main

import (
	"context"
	"strings"
)

type Test struct{}

func (m *Test) Hello(ctx context.Context) (string, error) {
	s, err := dag.Versioned().Hello(ctx)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(s), nil
}
