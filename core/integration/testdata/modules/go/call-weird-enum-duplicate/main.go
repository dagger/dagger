package main

type Status string

const (
	Active     Status = "ACTIVE"
	Inactive   Status = "INACTIVE"
	Duplicated Status = "ACTIVE"
)

type Test struct{}

func (m *Test) FromStatus(status Status) string {
	return string(status)
}

func (m *Test) ToStatus(status string) Status {
	return Status(status)
}
