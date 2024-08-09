// A Dagger module for saying hello world!

package main

import (
	"fmt"
)

func New(
	// +optional
	// +default="Hello"
	greeting string,
	// +optional
	// +default="World"
	name string,
) *MyModule {
	return &MyModule{
		Greeting: greeting,
		Name:     name,
	}
}

type MyModule struct {
	Greeting string
	Name     string
}

func (hello *MyModule) Message() string {
	return fmt.Sprintf("%s, %s!", hello.Greeting, hello.Name)
}
