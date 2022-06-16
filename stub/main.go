package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	modelOutput := flag.String("m", "", "modelOutput file")
	frontendOutput := flag.String("f", "", "modelOutput file")
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

	modelGen, err := ModelGen(pkg)
	if err != nil {
		panic(err)
	}

	if *modelOutput != "" {
		if err := os.MkdirAll(filepath.Dir(*modelOutput), 0755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(*modelOutput, modelGen, 0644); err != nil {
			panic(err)
		}
	} else {
		fmt.Println(string(modelGen))
	}

	frontendGen, err := FrontendGen(pkg)
	if err != nil {
		panic(err)
	}

	if *frontendOutput != "" {
		if err := os.MkdirAll(filepath.Dir(*frontendOutput), 0755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(*frontendOutput, frontendGen, 0644); err != nil {
			panic(err)
		}
	} else {
		fmt.Println(string(frontendGen))
	}
}
