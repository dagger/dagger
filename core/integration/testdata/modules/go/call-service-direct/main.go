package main

import (
	"context"
	"dagger/test/internal/dagger"
	"fmt"
	"strings"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context, svc *dagger.Service) (string, error) {
	ports, err := svc.Ports(ctx)
	if err != nil {
		return "", err
	}
	var out []string
	out = append(out, fmt.Sprintf("%d exposed ports:", len(ports)))
	for _, port := range ports {
		number, err := port.Port(ctx)
		if err != nil {
			return "", err
		}
		out = append(out, fmt.Sprintf("- TCP/%d", number))
	}
	return strings.Join(out, "\n"), nil
}
