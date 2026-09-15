package main

import "strings"

type Test struct{}

type Message struct {
	Content string
}

func (m *Test) SayHello(name string) Message {
	return Message{Content: "hello " + name}
}

func (m *Test) Upper(msg Message) Message {
	msg.Content = strings.ToUpper(msg.Content)
	return msg
}

func (m *Test) Uppers(msg []Message) []Message {
	for i := range msg {
		msg[i].Content = strings.ToUpper(msg[i].Content)
	}
	return msg
}
