package main

type Status string

const (
	Value Status = "1ACTIVE"
)

type Test struct{}

func (m *Test) FromStatus(status Status) string {
	return string(status)
}
