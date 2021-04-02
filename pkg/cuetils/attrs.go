package cuetils

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/format"
)

func Hack(value cue.Value) error {

	var err error

	// Validate the value
	err = value.Validate()
	if err != nil {
		fmt.Println("Error during validate:", err)
		return err
	}

	// Generate an AST
	//   try out different options
	syn := value.Syntax(
		// cue.Final(), // close structs and lists
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
		return err
	}

	fmt.Println(string(bs))

	// Process inputs
	err = findAttrs(value)
	if err != nil {
		fmt.Println("Error:", err)
		return err
	}

	return nil
}

func findAttrs(val cue.Value) error {
	before := func(v cue.Value) (bool, error) {
		label, _ := v.Label()
		attrs := v.Attributes(cue.ValueAttr)
		for _, attr := range attrs {
			name :=  attr.Name()
			if name == "input" {
				fmt.Println("input: ", label)
				for i := 0; i < attr.NumArgs(); i++ {
					k, v := attr.Arg(i)
					fmt.Println(" -", k, v)
				}
			}
			if name == "output" {
				fmt.Println("output: ", label, attrs)
			}
		}
		return true, nil
	}

	err := walkValue(val, before, nil)
	if err != nil {
		return err
	}

	return nil
}

func walkValue(val cue.Value, before func (cue.Value) (bool, error), after func(cue.Value) error) error {

	if before != nil {

		recurse, err := before(val)
		if err != nil {
			return err
		}

		// should we recurse into fields
		if recurse {

			// is val a struct?
			if _, err := val.Struct(); err == nil {

				iter, err := val.Fields(
					cue.Definitions(true),
					cue.Optional(true),
				)
				if err != nil {
					return err
				}
				for iter.Next() {
					err := walkValue(iter.Value(), before, after)
					if err != nil {
						return err
					}
				}

			}
		}
	}

	if after != nil {
		err := after(val)
		if err != nil {
			return err
		}
	}

	return nil
}

