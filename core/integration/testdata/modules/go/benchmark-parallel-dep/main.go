package main

import (
	"context"

	"golang.org/x/sync/errgroup"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context) ([]string, error) {
	var eg errgroup.Group
	results := make([]string, 10)
	for i := 0; i < 10; i++ {
		eg.Go(func() error {
			res, err := dag.Dep().DepFn(ctx, dag.SetSecret("foo", "bar"))
			if err != nil {
				return err
			}
			results[i] = res
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}
