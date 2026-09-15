package main

import (
	"fmt"
)

func New() (*Test, error) {
	return nil, fmt.Errorf("too bad: %s", "so sad")
}

type Test struct {
	Foo string
}
