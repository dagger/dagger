package main

import "fmt"

type Potato struct{}

func (m *Potato) HelloWorld(
	// the number of potatoes to process
	count int,
	// whether the potatoes are mashed (this is an optional parameter!)
	mashed Optional[bool],
) string {
	if mashed.GetOr(false) {
		return fmt.Sprintf("Hello world, I have mashed %d potatoes", count)
	}
	return fmt.Sprintf("Hello world, I have %d potatoes", count)
}
