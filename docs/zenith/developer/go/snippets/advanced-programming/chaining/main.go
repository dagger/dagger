// A Dagger module for saying hello world!

package main

import (
	"context"
	"fmt"
)

type HelloWorld struct {
	Greeting string
	Name     string
}

func (hello *HelloWorld) WithGreeting(ctx context.Context, greeting string) (*HelloWorld, error) {
	hello.Greeting = greeting
	return hello, nil
}

func (hello *HelloWorld) WithName(ctx context.Context, name string) (*HelloWorld, error) {
	hello.Name = name
	return hello, nil
}

func (hello *HelloWorld) Message(ctx context.Context) (string, error) {
	var (
		greeting = hello.Greeting
		name     = hello.Name
	)
	if greeting == "" {
		greeting = "Hello"
	}
	if name == "" {
		name = "World"
	}
	return fmt.Sprintf("%s, %s!", greeting, name), nil
}
