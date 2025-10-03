package main

import "context"

type Foobar struct{}

func (m *Foobar) Exclaim(
	ctx context.Context,
	message string,
	// +default=1
	count int,
) string {
	result := message
	for i := 0; i < count; i++ {
		result += "!"
	}
	return result
}
