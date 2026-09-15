package main

import "crypto/rand"

type Test struct{}

func (m *Test) TestAlwaysCache() string {
	return rand.Text()
}
