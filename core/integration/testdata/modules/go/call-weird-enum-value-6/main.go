package main

type Status string

const (
	Value Status = "foo bar"
)

type Test struct{}

func (m *Test) FromStatus(status Status) string {
	return string(status)
}
