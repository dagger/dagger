package main

type Status string

const (
	Value Status = "ACTIVE "
)

type Test struct{}

func (m *Test) FromStatus(status Status) string {
	return string(status)
}
