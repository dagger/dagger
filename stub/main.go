package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <file>\n", os.Args[0])
		os.Exit(1)
	}
	pkg, err := Parse(os.Args[1])
	if err != nil {
		panic(err)
	}

	gen, err := Stub(pkg)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(gen))
}
