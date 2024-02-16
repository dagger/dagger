package main

type Potato struct{}

type PotatoMessage struct {
	Message string
	From    string
}

func (m *Potato) HelloWorld(message string) PotatoMessage {
	return PotatoMessage{
		Message: message,
		From:    "potato@example.com",
	}
}
