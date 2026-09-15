package main

import (
	"context"
	"fmt"
)

type RollsRoyce struct{}

func (r *RollsRoyce) Drive(ctx context.Context) error {
	fmt.Println("I'm a rolls royce")
	return nil
}
