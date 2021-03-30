package main

import (
	"fmt"
	"os"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/load"
)

// We need a Cue.Runtime, the zero value is ready to use
var RT cue.Runtime

const help = "please supply a varian choice from [defn,attr]"

func main() {
	if len(os.Args) != 2 {
		fmt.Println(help)
		os.Exit(1)
	}

	var entrypoints []string

	variant := os.Args[1]
	switch variant {
		case "defn":
			entrypoints = []string{"./defn/"}

		case "attr":
			entrypoints = []string{"./attr/"}

		default:
			fmt.Println("unknown variant %q", variant)
			fmt.Println(help)
			os.Exit(1)
	}

	var value cue.Value

	// Load Cue files into Cue build.Instances slice
	// the second arg is a configuration object, we'll see this later
	bis := load.Instances(entrypoints, nil)

	// Loop over the instances, checking for errors and printing
	for _, bi := range bis {
		// check for errors on the instance
		// these are typically parsing errors
		if bi.Err != nil {
			fmt.Println("Error during load:", bi.Err)
			continue
		}

		// Use cue.Runtime to build.Instance to cue.INstance
		I, err := RT.Build(bi)
		if err != nil {
			fmt.Println("Error during build:", bi.Err)
			continue
		}

		// get the root value and print it
		value = I.Value()

		// Validate the value
		err = value.Validate()
		if err != nil {
			fmt.Println("Error during validate:", err)
			continue
		}

		// Generate an AST
		//   try out different options
		syn := value.Syntax(
			cue.Final(), // close structs and lists
			cue.Concrete(false),   // allow incomplete values
			cue.Definitions(true),
			cue.Hidden(true),
			cue.Optional(true),
			cue.Attributes(true),
			cue.Docs(true),
		)

		// Pretty print the AST, returns ([]byte, error)
		bs, err := format.Node(
			syn,
			format.TabIndent(false),
			format.UseSpaces(2),
			// format.Simplify(),
		)
		if err != nil {
			fmt.Println("Error during format:", err)
			continue
		}

		fmt.Println(string(bs))
	}

	var err error
	// Process inputs
	switch variant {
		case "attr":
			err = findAttrs(value)

		case "defn":
			err = findDefns(value)
	}

	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}

func findAttrs(val cue.Value) error {
	before := func(v cue.Value) bool {
		fmt.Println(v)
		return true
	}

	val.Walk(before, nil)

	return nil
}

func findDefns(val cue.Value) error {

	return nil
}
