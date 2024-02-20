// A Dagger module for saying hello world!

package main

import (
	"fmt"
)

type HelloWorld struct {
}

func (*HelloWorld) Divide(a, b int) (int, error) {
	if b == 0 {
		return 0, fmt.Errorf("cannot divide by zero")
	}
	return a / b, nil
}
