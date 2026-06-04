// A Dagger module for saying hello world!

package main

import (
	"context"
	"fmt"
)

type MyModule struct {
	Greeting string
	Name     string
}

func (hello *MyModule) WithGreeting(ctx context.Context, greeting string) (*MyModule, error) {
	hello.Greeting = greeting
	return hello, nil
}

func (hello *MyModule) WithName(ctx context.Context, name string) (*MyModule, error) {
	hello.Name = name
	return hello, nil
}

func (hello *MyModule) Message(ctx context.Context) (string, error) {
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
