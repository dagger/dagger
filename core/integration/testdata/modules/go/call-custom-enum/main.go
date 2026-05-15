package main

type Status string

const (
	Active   Status = "ACTIVE"
	Inactive Status = "INACTIVE"
)

type Test struct{}

func (m *Test) FromStatus(
	// +default="INACTIVE"
	status Status,
) string {
	return string(status)
}

func (m *Test) ToStatus(status string) Status {
	return Status(status)
}
