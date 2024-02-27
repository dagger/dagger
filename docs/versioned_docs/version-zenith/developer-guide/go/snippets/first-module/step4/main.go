package main

import "fmt"

type Potato struct{}

type PotatoMessage struct {
	Message string
	From    string
}

func (m *Potato) HelloWorld(
	// the number of potatoes to process
	count int,

	// whether the potatoes are mashed
	// +optional
	mashed bool,
) PotatoMessage {
	if mashed {
		return PotatoMessage{
			Message: fmt.Sprintf("Hello Daggernauts, I have mashed %d potatoes", count),
			From:    "potato@example.com",
		}
	}

	return PotatoMessage{
		Message: fmt.Sprintf("Hello Daggernauts, I have %d potatoes", count),
		From:    "potato@example.com",
	}
}
