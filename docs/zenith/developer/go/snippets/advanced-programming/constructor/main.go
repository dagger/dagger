// A Dagger module for saying hello world!

package main

import (
	"fmt"
)

func New(greeting Optional[string], name Optional[string]) *HelloWorld {
	return &HelloWorld{
		Greeting: greeting.GetOr("Hello"),
		Name:     name.GetOr("World"),
	}
}

type HelloWorld struct {
	Greeting string
	Name     string
}

func (hello *HelloWorld) Message() string {
	return fmt.Sprintf("%s, %s!", hello.Greeting, hello.Name)
}
