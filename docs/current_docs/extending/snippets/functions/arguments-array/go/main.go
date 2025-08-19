package main

import (
	"strings"
)

type MyModule struct{}

func (m *MyModule) Hello(names []string) string {
	message := "Hello"
	if len(names) > 0 {
		message += " " + strings.Join(names, ", ")
	}

	return message
}
