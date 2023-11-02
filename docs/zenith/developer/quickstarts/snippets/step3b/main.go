package main

type Potato struct{}

// HACK: to be queried, custom object fields require `json` tags
type PotatoMessage struct {
	Message string `json:"message"`
	From    string `json:"from"`
}

// HACK: this is temporarily required to ensure that the codegen discovers
// PotatoMessage
func (msg PotatoMessage) Void() {}

func (m *Potato) HelloWorld(message string) PotatoMessage {
	return PotatoMessage{
		Message: message,
		From:    "potato@example.com",
	}
}
