// A Dagger module for saying hello world!

package main

import (
	"fmt"
)

func New(
	// +default=Hello
	greeting string,
	// +default=World
	name string,
) *HelloWorld {
	return &HelloWorld{
		Greeting: greeting,
		Name:     name,
	}
}

type HelloWorld struct {
	Greeting string
	Name     string
}

func (hello *HelloWorld) Message() string {
	return fmt.Sprintf("%s, %s!", hello.Greeting, hello.Name)
}
