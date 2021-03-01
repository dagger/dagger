package main

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/load"
)

func main() {
	// We need a Cue.Runtime, the zero value is ready to use
	var RT cue.Runtime

	// The entrypoints are the same as the files you'd specify at the command line
	entrypoints := []string{"repro.cue"}

	// Load Cue files into Cue build.Instances slice
	// the second arg is a configuration object, we'll see this later
	bis := load.Instances(entrypoints, nil)

	var (
		I *cue.Instance
		V cue.Value
		err error
	)


	// Loop over the instances, checking for errors and printing
	for _, bi := range bis {
		// check for errors on the instance
		// these are typically parsing errors
		if bi.Err != nil {
			fmt.Println("Error during load:", bi.Err)
			continue
		}

		// Use cue.Runtime to build.Instance to cue.INstance
		I, err = RT.Build(bi)
		if err != nil {
			fmt.Println("Error during build:", bi.Err)
			continue
		}

		// get the root value and print it
		V = I.Value()
		// fmt.Println("root value:", V)

		// Validate the value
		err = V.Validate()
		if err != nil {
			fmt.Println("Error during validate:", err)
			continue
		}
	}

	empty, err := RT.Compile("", "")
	if err != nil {
		panic(err)
	}
	// fmt.Println("empty:", empty.Value())

	base := I.Lookup("base")
	// fmt.Println("base:", base)

	input := I.Lookup("input")
	// fmt.Println("input:", input)

	output := I.Lookup("output")
	// fmt.Println("output:", output)

	// fmt.Println("===============")

	merged := empty

	fmt.Println("merge.base")
	merged, err = merged.Fill(base)
	if err != nil {
		panic(err)
	}

	fmt.Println("merge.input")
	merged, err = merged.Fill(input)
	if err != nil {
		panic(err)
	}

	fmt.Println("merge.output")
	merged, err = merged.Fill(output)
	if err != nil {
		panic(err)
	}

	fmt.Println("merge.value")
	final := merged.Value()

	fmt.Println("merge.final")
	fmt.Println(final)

}

