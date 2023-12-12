package main

import (
	"context"
	"fmt"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context, s string) ([]string, error) {
	var sub1Obj *Sub1Obj
	sub1Obj = dag.Sub1().Fn(s)
	s1, err := sub1Obj.GetFoo(ctx)
	if err != nil {
		return nil, err
	}

	var sub2Obj *Sub2Obj
	sub2Obj = dag.Sub2().Fn(s)
	s2, err := sub2Obj.GetBar(ctx)
	if err != nil {
		return nil, err
	}

	return []string{
		fmt.Sprintf("%T made %s", sub1Obj, s1),
		fmt.Sprintf("%T made %s", sub2Obj, s2),
	}, nil
}
