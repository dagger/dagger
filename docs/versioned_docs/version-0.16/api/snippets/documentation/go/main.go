// A simple example module to say hello.

// Further documentation for the module here.

package main

import (
	"fmt"
	"strings"
)

type MyModule struct{}

// Return a greeting.
func (m *MyModule) Hello(
	// Who to greet
	name string,
	// The greeting to display
	greeting string,
) string {
	return fmt.Sprintf("%s, %s!", greeting, name)
}

// Return a loud greeting.
func (m *MyModule) LoudHello(
	// Who to greet
	name string,
	// The greeting to display
	greeting string,
) string {
	out := fmt.Sprintf("%s, %s!", greeting, name)
	return strings.ToUpper(out)
}
