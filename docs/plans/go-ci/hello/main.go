package main

import (
	"fmt"
	"os"

	"dagger.io/testgreetci/greeting"
)

func main() {
	name := os.Getenv("NAME")
	if name == "" {
		name = "John Doe"
	}
	fmt.Printf(greeting.Greeting(name))
}
