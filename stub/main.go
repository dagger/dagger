package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	output := flag.String("o", "", "output file")
	flag.Parse()

	args := flag.Args()

	projectFile := "dagger.cue"
	if len(args) > 0 {
		projectFile = args[0]
	}
	pkg, err := Parse(projectFile)
	if err != nil {
		panic(err)
	}

	gen, err := Stub(pkg)
	if err != nil {
		panic(err)
	}

	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(*output, gen, 0644); err != nil {
			panic(err)
		}
	} else {
		fmt.Println(string(gen))
	}
}
