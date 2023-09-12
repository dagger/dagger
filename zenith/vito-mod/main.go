package main

import "context"

type Vito struct{}

func (m *Vito) HelloWorld(context.Context) (string, error) {
	return "hey", nil
}
