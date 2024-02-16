package main

import "fmt"

type Potato struct{}

func (m *Potato) HelloWorld(
	// the number of potatoes to process
	count int,

	// whether the potatoes are mashed
	// +optional
	mashed bool,
) string {
	if mashed {
		return fmt.Sprintf("Hello world, I have mashed %d potatoes", count)
	}
	return fmt.Sprintf("Hello world, I have %d potatoes", count)
}
