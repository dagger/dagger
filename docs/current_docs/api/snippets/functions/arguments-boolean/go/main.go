package main

import (
	"strings"
)

type MyModule struct{}

func (m *MyModule) Hello(shout bool) string {
	message := "Hello, world"
	if shout {
		return strings.ToUpper(message)
	}
	return message
}
